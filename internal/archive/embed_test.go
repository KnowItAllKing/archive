package archive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeEmbedder struct {
	calls int
	texts []string
}

func (f *fakeEmbedder) ID() string {
	return "fake:v1"
}

// Texts mentioning identity concepts land on one axis, everything else on the
// other, so semantic similarity is deterministic without a real model.
func (f *fakeEmbedder) Embed(texts []string) ([][]float32, error) {
	f.calls++
	f.texts = append(f.texts, texts...)
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		lower := strings.ToLower(text)
		if strings.Contains(lower, "sso") || strings.Contains(lower, "login") || strings.Contains(lower, "identity") {
			vectors[i] = []float32{1, 0}
		} else {
			vectors[i] = []float32{0, 1}
		}
	}
	return vectors, nil
}

func addIdentityAndPastaEntries(t *testing.T, store *Store) (Entry, Entry) {
	t.Helper()
	identity, err := store.Add(AddInput{
		Title: "Identity provider setup", Category: "infra", Tags: []string{"sso", "oidc"},
		Body: "The identity provider handles SSO for internal apps.\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	pasta, err := store.Add(AddInput{
		Title: "Pasta technique", Category: "personal", Tags: []string{"cooking"},
		Body: "Salt the water generously and finish in the sauce.\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	return identity, pasta
}

func TestSemanticSearchFindsNonLexicalMatch(t *testing.T) {
	store := newTestStore(t)
	store.Embedder = &fakeEmbedder{}
	identity, _ := addIdentityAndPastaEntries(t, store)

	lexical, err := store.Search("login", "", 10, ModeLexical)
	if err != nil {
		t.Fatal(err)
	}
	if len(lexical) != 0 {
		t.Fatalf("lexical results for 'login' = %#v", lexical)
	}

	semantic, err := store.Search("login", "", 10, ModeSemantic)
	if err != nil {
		t.Fatal(err)
	}
	if len(semantic) != 2 || semantic[0].ID != identity.ID {
		t.Fatalf("semantic results = %#v", semantic)
	}
	if semantic[0].Score <= semantic[1].Score {
		t.Fatalf("scores not descending: %#v", semantic)
	}

	hybrid, err := store.Search("login", "", 10, ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if len(hybrid) == 0 || hybrid[0].ID != identity.ID {
		t.Fatalf("hybrid results = %#v", hybrid)
	}
}

func TestHybridFusionPrefersAgreement(t *testing.T) {
	lexical := []SearchResult{{ID: "a"}, {ID: "b"}}
	semantic := []SearchResult{{ID: "b"}, {ID: "c"}}
	fused := fuseRRF(lexical, semantic, 10)
	if len(fused) != 3 || fused[0].ID != "b" {
		t.Fatalf("fused = %#v", fused)
	}
	if fused[0].Rank != 1 || fused[2].Rank != 3 {
		t.Fatalf("ranks = %#v", fused)
	}
}

func TestEmbeddingCacheIsIncremental(t *testing.T) {
	store := newTestStore(t)
	fake := &fakeEmbedder{}
	store.Embedder = fake
	identity, _ := addIdentityAndPastaEntries(t, store)
	entryTexts := len(fake.texts)
	if entryTexts != 2 {
		t.Fatalf("embedded %d texts for 2 adds, want 2", entryTexts)
	}

	if _, err := store.Reindex(); err != nil {
		t.Fatal(err)
	}
	if len(fake.texts) != entryTexts {
		t.Fatalf("reindex re-embedded cached entries: %v", fake.texts)
	}

	if _, err := store.Update(identity.ID, UpdateInput{Body: "Identity provider notes, revised.\n"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.texts) != entryTexts+1 {
		t.Fatalf("update embedded %d new texts, want 1", len(fake.texts)-entryTexts)
	}

	entries, err := store.readAllEntries()
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := store.countEmbedded(entries)
	if err != nil {
		t.Fatal(err)
	}
	if embedded != 2 {
		t.Fatalf("embedded coverage = %d, want 2 (stale vector should be pruned)", embedded)
	}
}

func TestSemanticSearchWithoutEmbedderFails(t *testing.T) {
	store := newTestStore(t)
	addTestEntry(t, store)
	if _, err := store.Search("keycloak", "", 10, ModeSemantic); err == nil || !strings.Contains(err.Error(), "semantic search is unavailable") {
		t.Fatalf("semantic error = %v", err)
	}
}

func TestRemoteEmbedder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		var request struct {
			Input []string `json:"input"`
			Model string   `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "test-model" || len(request.Input) != 2 {
			t.Errorf("request = %#v", request)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 1, "embedding": []float64{0, 3}},
				{"index": 0, "embedding": []float64{4, 0}},
			},
		})
	}))
	defer server.Close()

	embedder := newRemoteEmbedder(server.URL+"/v1", "test-model", "secret")
	vectors, err := embedder.Embed([]string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 {
		t.Fatalf("vectors not ordered by index and normalized: %#v", vectors)
	}
}

func TestVectorBlobRoundTrip(t *testing.T) {
	vector := []float32{0.25, -1.5, 3}
	decoded, err := decodeVector(encodeVector(vector))
	if err != nil {
		t.Fatal(err)
	}
	for i := range vector {
		if decoded[i] != vector[i] {
			t.Fatalf("round trip = %v", decoded)
		}
	}
	if _, err := decodeVector([]byte{1, 2, 3}); err == nil {
		t.Fatal("invalid blob accepted")
	}
}
