package archive

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const embedBatchSize = 16

// The vector cache is persistent, unlike the FTS index: embeddings are
// expensive to recompute, so reindexing only embeds content whose hash is
// missing and prunes rows whose content no longer exists.
func (s *Store) openVectorCache() (*sql.DB, error) {
	db, err := sql.Open("sqlite", filepath.Join(s.Root, ".index", "embeddings.db"))
	if err != nil {
		return nil, fmt.Errorf("open vector cache: %w", err)
	}
	statements := []string{
		`PRAGMA journal_mode=DELETE`,
		`CREATE TABLE IF NOT EXISTS vectors (
			model TEXT NOT NULL,
			hash TEXT NOT NULL,
			vector BLOB NOT NULL,
			PRIMARY KEY (model, hash)
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize vector cache: %w", err)
		}
	}
	return db, nil
}

func (s *Store) cachedHashes(db *sql.DB, model string) (map[string]bool, error) {
	rows, err := db.Query(`SELECT hash FROM vectors WHERE model = ?`, model)
	if err != nil {
		return nil, fmt.Errorf("read vector cache: %w", err)
	}
	defer rows.Close()
	hashes := map[string]bool{}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, fmt.Errorf("read vector cache row: %w", err)
		}
		hashes[hash] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read vector cache rows: %w", err)
	}
	return hashes, nil
}

func (s *Store) ensureEmbeddings(entries []Entry) error {
	if s.Embedder == nil {
		return nil
	}
	db, err := s.openVectorCache()
	if err != nil {
		return err
	}
	defer db.Close()
	model := s.Embedder.ID()
	cached, err := s.cachedHashes(db, model)
	if err != nil {
		return err
	}
	current := map[string]bool{}
	var missing []Entry
	for _, entry := range entries {
		hash := embeddingHash(entry)
		current[hash] = true
		if !cached[hash] {
			missing = append(missing, entry)
		}
	}
	for start := 0; start < len(missing); start += embedBatchSize {
		batch := missing[start:min(start+embedBatchSize, len(missing))]
		texts := make([]string, len(batch))
		for i, entry := range batch {
			texts[i] = embeddingText(entry)
		}
		vectors, err := s.Embedder.Embed(texts)
		if err != nil {
			return err
		}
		for i, entry := range batch {
			if _, err := db.Exec(
				`INSERT OR REPLACE INTO vectors (model, hash, vector) VALUES (?, ?, ?)`,
				model, embeddingHash(entry), encodeVector(vectors[i]),
			); err != nil {
				return fmt.Errorf("cache vector for %q: %w", entry.ID, err)
			}
		}
	}
	for hash := range cached {
		if !current[hash] {
			if _, err := db.Exec(`DELETE FROM vectors WHERE model = ? AND hash = ?`, model, hash); err != nil {
				return fmt.Errorf("prune stale vector: %w", err)
			}
		}
	}
	return nil
}

func (s *Store) loadVectors(entries []Entry) (map[string][]float32, error) {
	db, err := s.openVectorCache()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	model := s.Embedder.ID()
	vectors := map[string][]float32{}
	for _, entry := range entries {
		var blob []byte
		err := db.QueryRow(
			`SELECT vector FROM vectors WHERE model = ? AND hash = ?`,
			model, embeddingHash(entry),
		).Scan(&blob)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("entry %q has no cached vector; run archive reindex", entry.ID)
		}
		if err != nil {
			return nil, fmt.Errorf("load vector for %q: %w", entry.ID, err)
		}
		vector, err := decodeVector(blob)
		if err != nil {
			return nil, fmt.Errorf("decode vector for %q: %w", entry.ID, err)
		}
		vectors[entry.ID] = vector
	}
	return vectors, nil
}

func (s *Store) countEmbedded(entries []Entry) (int, error) {
	if s.Embedder == nil {
		return 0, nil
	}
	db, err := s.openVectorCache()
	if err != nil {
		return 0, err
	}
	defer db.Close()
	cached, err := s.cachedHashes(db, s.Embedder.ID())
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if cached[embeddingHash(entry)] {
			count++
		}
	}
	return count, nil
}

func (s *Store) searchSemantic(query, category string, limit int) ([]SearchResult, error) {
	if s.Embedder == nil {
		return nil, errors.New("semantic search is unavailable: embeddings are disabled (ARCHIVE_EMBEDDINGS=off)")
	}
	entries, err := s.readAllEntries()
	if err != nil {
		return nil, err
	}
	if category != "" {
		filtered := entries[:0]
		for _, entry := range entries {
			if entry.Category == category {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}
	if len(entries) == 0 {
		return []SearchResult{}, nil
	}
	if err := s.ensureEmbeddings(entries); err != nil {
		return nil, err
	}
	queryVectors, err := s.Embedder.Embed([]string{query})
	if err != nil {
		return nil, err
	}
	vectors, err := s.loadVectors(entries)
	if err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(entries))
	for _, entry := range entries {
		results = append(results, SearchResult{
			ID: entry.ID, Title: entry.Title, Category: entry.Category, Tags: entry.Tags,
			Score:   dot(queryVectors[0], vectors[entry.ID]),
			Snippet: bodyPrefix(entry.Body, 25),
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].ID < results[j].ID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	for i := range results {
		results[i].Rank = i + 1
	}
	return results, nil
}

func fuseRRF(lexical, semantic []SearchResult, limit int) []SearchResult {
	const k = 60.0
	scores := map[string]float64{}
	byID := map[string]SearchResult{}
	for _, list := range [][]SearchResult{lexical, semantic} {
		for i, result := range list {
			scores[result.ID] += 1.0 / (k + float64(i+1))
			if _, ok := byID[result.ID]; !ok {
				byID[result.ID] = result
			}
		}
	}
	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] == scores[ids[j]] {
			return ids[i] < ids[j]
		}
		return scores[ids[i]] > scores[ids[j]]
	})
	if len(ids) > limit {
		ids = ids[:limit]
	}
	results := make([]SearchResult, len(ids))
	for i, id := range ids {
		result := byID[id]
		result.Score = scores[id]
		result.Rank = i + 1
		results[i] = result
	}
	return results
}

func bodyPrefix(body string, words int) string {
	fields := strings.Fields(body)
	if len(fields) > words {
		return strings.Join(fields[:words], " ") + "..."
	}
	return strings.Join(fields, " ")
}
