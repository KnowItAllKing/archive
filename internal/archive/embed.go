package archive

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultLocalModel = "sentence-transformers/all-MiniLM-L6-v2"

type Embedder interface {
	ID() string
	Embed(texts []string) ([][]float32, error)
}

// NewEmbedderFromEnv reads ARCHIVE_EMBEDDINGS: unset or "local" selects the
// built-in local model, "off" disables vector search (returns nil), and a URL
// selects an OpenAI-compatible /v1/embeddings endpoint such as LM Studio.
func NewEmbedderFromEnv() (Embedder, error) {
	backend := strings.TrimSpace(os.Getenv("ARCHIVE_EMBEDDINGS"))
	model := strings.TrimSpace(os.Getenv("ARCHIVE_EMBEDDINGS_MODEL"))
	switch {
	case backend == "off":
		return nil, nil
	case backend == "" || backend == "local":
		if model == "" {
			model = defaultLocalModel
		}
		return newLocalEmbedder(model)
	case strings.HasPrefix(backend, "http://") || strings.HasPrefix(backend, "https://"):
		if model == "" {
			return nil, errors.New("ARCHIVE_EMBEDDINGS_MODEL must name the model served by the remote embeddings endpoint")
		}
		return newRemoteEmbedder(backend, model, os.Getenv("ARCHIVE_EMBEDDINGS_API_KEY")), nil
	default:
		return nil, fmt.Errorf("invalid ARCHIVE_EMBEDDINGS %q: use \"local\", \"off\", or an http(s) endpoint URL", backend)
	}
}

type remoteEmbedder struct {
	url    string
	model  string
	apiKey string
	client *http.Client
}

func newRemoteEmbedder(endpoint, model, apiKey string) *remoteEmbedder {
	url := strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(url, "/embeddings") {
		url += "/embeddings"
	}
	return &remoteEmbedder{
		url: url, model: model, apiKey: strings.TrimSpace(apiKey),
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (e *remoteEmbedder) ID() string {
	return "remote:" + e.model
}

// Embed retries transient endpoint failures: local inference servers with
// just-in-time model loading (LM Studio) can answer 400 "Model is unloaded"
// when a request races an auto-unload, and a retry triggers the reload.
func (e *remoteEmbedder) Embed(texts []string) ([][]float32, error) {
	var body []byte
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Duration(attempt-1) * 2 * time.Second)
		}
		body, lastErr = e.request(texts)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse embeddings response: %w", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings endpoint returned %d vectors for %d inputs", len(parsed.Data), len(texts))
	}
	vectors := make([][]float32, len(texts))
	for _, item := range parsed.Data {
		if item.Index < 0 || item.Index >= len(texts) {
			return nil, fmt.Errorf("embeddings endpoint returned invalid index %d", item.Index)
		}
		if len(item.Embedding) == 0 {
			return nil, fmt.Errorf("embeddings endpoint returned an empty vector at index %d", item.Index)
		}
		vectors[item.Index] = normalize(item.Embedding)
	}
	for i, vector := range vectors {
		if vector == nil {
			return nil, fmt.Errorf("embeddings endpoint returned no vector for index %d", i)
		}
	}
	return vectors, nil
}

func (e *remoteEmbedder) request(texts []string) ([]byte, error) {
	payload, err := json.Marshal(map[string]any{"input": texts, "model": e.model})
	if err != nil {
		return nil, fmt.Errorf("encode embeddings request: %w", err)
	}
	request, err := http.NewRequest(http.MethodPost, e.url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build embeddings request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+e.apiKey)
	}
	response, err := e.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call embeddings endpoint %s: %w", e.url, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("read embeddings response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings endpoint %s returned %s: %s", e.url, response.Status, truncate(string(body), 300))
	}
	return body, nil
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

func embeddingText(entry Entry) string {
	return entry.Title + "\n" + strings.Join(entry.Tags, " ") + "\n" + entry.Body
}

func embeddingHash(entry Entry) string {
	sum := sha256.Sum256([]byte(embeddingText(entry)))
	return hex.EncodeToString(sum[:])
}

func normalize(vector []float32) []float32 {
	var sum float64
	for _, value := range vector {
		sum += float64(value) * float64(value)
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return vector
	}
	normalized := make([]float32, len(vector))
	for i, value := range vector {
		normalized[i] = float32(float64(value) / norm)
	}
	return normalized
}

func dot(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

func encodeVector(vector []float32) []byte {
	data := make([]byte, 4*len(vector))
	for i, value := range vector {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(value))
	}
	return data
}

func decodeVector(data []byte) ([]float32, error) {
	if len(data) == 0 || len(data)%4 != 0 {
		return nil, fmt.Errorf("invalid vector blob of %d bytes", len(data))
	}
	vector := make([]float32, len(data)/4)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return vector, nil
}
