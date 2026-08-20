package archive

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJotCapturesToInbox(t *testing.T) {
	store := newTestStore(t)
	entry, err := store.Jot("keycloak scopes might be unnecessary on the ingest client, verify against staging before removing them", "session:test")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Category != "inbox" || !contains(entry.Tags, "jot") {
		t.Fatalf("jot entry = %#v", entry)
	}
	if entry.Title != "keycloak scopes might be unnecessary on the ingest" {
		t.Fatalf("jot title = %q", entry.Title)
	}
	if !strings.Contains(entry.Body, "verify against staging") {
		t.Fatalf("jot body truncated: %q", entry.Body)
	}

	status, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Jots != 1 {
		t.Fatalf("jots = %d", status.Jots)
	}
	backlog, err := store.List("", "jot", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(backlog) != 1 || backlog[0].ID != entry.ID {
		t.Fatalf("backlog = %#v", backlog)
	}

	if _, err := store.Jot("   ", ""); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty jot error = %v", err)
	}
}

func TestReviewLifecycle(t *testing.T) {
	store := newTestStore(t)
	entry, err := store.Add(AddInput{
		Title: "Perishable claim", Category: "infra", Review: "2026-08-19",
		Body: "Removing the scope still worked; verify before trusting.\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	status, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.NeedsReview != 1 {
		t.Fatalf("needs review = %d", status.NeedsReview)
	}
	due, err := store.List("", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != entry.ID {
		t.Fatalf("due = %#v", due)
	}

	if _, err := store.Update(entry.ID, UpdateInput{KeepBody: true, Review: "", SetReview: true}); err != nil {
		t.Fatal(err)
	}
	status, err = store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.NeedsReview != 0 {
		t.Fatalf("needs review after clear = %d", status.NeedsReview)
	}

	if _, err := store.Add(AddInput{Title: "Bad date", Category: "inbox", Review: "soon", Body: "x\n"}); err == nil || !strings.Contains(err.Error(), "review date") {
		t.Fatalf("invalid review error = %v", err)
	}
}

func TestRelatedRanksBySimilarity(t *testing.T) {
	store := newTestStore(t)
	store.Embedder = &fakeEmbedder{}
	identity, _ := addIdentityAndPastaEntries(t, store)
	sibling, err := store.Add(AddInput{
		Title: "SSO login troubleshooting", Category: "infra", Tags: []string{"sso"},
		Body: "Login loops usually mean a clock skew on the identity provider.\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	results, err := store.Related(identity.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].ID != sibling.ID {
		t.Fatalf("related = %#v", results)
	}
	for _, result := range results {
		if result.ID == identity.ID {
			t.Fatal("related includes the entry itself")
		}
	}

	if _, err := store.Related("missing-id", 10); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing id error = %v", err)
	}
	store.Embedder = nil
	if _, err := store.Related(identity.ID, 10); err == nil || !strings.Contains(err.Error(), "requires embeddings") {
		t.Fatalf("no embedder error = %v", err)
	}
}

func TestSyncReconcilesHandEdits(t *testing.T) {
	store := newTestStore(t)
	entry := addTestEntry(t, store)
	if _, err := store.Jot("stray thought", ""); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(store.Root, "entries", "infra", entry.ID+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), "The GCIP scope question.", "Completely rewritten wisdom.", 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := store.Sync(&output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "committed hand edits") || !strings.Contains(output.String(), "jot backlog: 1") {
		t.Fatalf("sync output = %q", output.String())
	}

	status, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Dirty) != 0 {
		t.Fatalf("dirty after sync = %v", status.Dirty)
	}
	results, err := store.Search("rewritten wisdom", "", 10, ModeLexical)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != entry.ID {
		t.Fatalf("hand edit not indexed: %#v", results)
	}

	broken := strings.Replace(edited, "category: infra", "category: nonsense", 1)
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.Sync(&bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "store validation failed") {
		t.Fatalf("broken edit error = %v", err)
	}
	logOutput := gitOutput(t, store.Root, "log", "-1", "--format=%s")
	if strings.Contains(logOutput, "sync") && strings.Contains(logOutput, "hand") && strings.Count(gitOutput(t, store.Root, "log", "--format=%s", "--grep", "sync: hand edits"), "\n") > 1 {
		t.Fatalf("broken edit was committed: %q", logOutput)
	}
}
