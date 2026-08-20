package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/nlpodyssey/cybertron/pkg/models/bert"
	"github.com/nlpodyssey/cybertron/pkg/tasks"
	"github.com/nlpodyssey/cybertron/pkg/tasks/textencoding"
)

type localEmbedder struct {
	model string
	once  sync.Once
	enc   textencoding.Interface
	err   error
}

func newLocalEmbedder(model string) (*localEmbedder, error) {
	return &localEmbedder{model: model}, nil
}

func (e *localEmbedder) ID() string {
	return "local:" + e.model
}

// load defers model loading to first use so commands that never embed do not
// pay the startup (or first-run download) cost.
func (e *localEmbedder) load() (textencoding.Interface, error) {
	e.once.Do(func() {
		cacheDir, err := os.UserCacheDir()
		if err != nil {
			e.err = fmt.Errorf("find user cache directory for embedding models: %w", err)
			return
		}
		modelsDir := filepath.Join(cacheDir, "archive", "models")
		if err := os.MkdirAll(modelsDir, 0o755); err != nil {
			e.err = fmt.Errorf("create embedding model directory: %w", err)
			return
		}
		if _, err := os.Stat(filepath.Join(modelsDir, e.model)); err != nil {
			fmt.Fprintf(os.Stderr, "archive: downloading embedding model %s to %s (first run only)\n", e.model, modelsDir)
		}
		encoder, err := tasks.Load[textencoding.Interface](&tasks.Config{
			ModelsDir: modelsDir,
			ModelName: e.model,
		})
		if err != nil {
			e.err = fmt.Errorf("load local embedding model %s: %w", e.model, err)
			return
		}
		e.enc = encoder
	})
	return e.enc, e.err
}

func (e *localEmbedder) Embed(texts []string) ([][]float32, error) {
	encoder, err := e.load()
	if err != nil {
		return nil, err
	}
	vectors := make([][]float32, len(texts))
	for i, text := range texts {
		result, err := encoder.Encode(context.Background(), text, int(bert.MeanPooling))
		if err != nil {
			return nil, fmt.Errorf("embed text with %s: %w", e.model, err)
		}
		values := result.Vector.Data().F64()
		vector := make([]float32, len(values))
		for j, value := range values {
			vector[j] = float32(value)
		}
		vectors[i] = normalize(vector)
	}
	return vectors, nil
}
