package archive

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	_ "modernc.org/sqlite"
)

type SearchResult struct {
	Rank     int      `json:"rank"`
	Score    float64  `json:"score"`
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Category string   `json:"category"`
	Tags     []string `json:"tags"`
	Snippet  string   `json:"snippet"`
}

func (s *Store) Reindex() (int, error) {
	if err := s.requireInitialized(); err != nil {
		return 0, err
	}
	entries, err := s.readAllEntries()
	if err != nil {
		return 0, err
	}
	indexDir := filepath.Join(s.Root, ".index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return 0, fmt.Errorf("create index directory: %w", err)
	}
	temporary, err := os.CreateTemp(indexDir, "archive-*.db")
	if err != nil {
		return 0, fmt.Errorf("create temporary index: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return 0, fmt.Errorf("close temporary index file: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return 0, fmt.Errorf("prepare temporary index path: %w", err)
	}
	defer os.Remove(temporaryPath)

	db, err := sql.Open("sqlite", temporaryPath)
	if err != nil {
		return 0, fmt.Errorf("open temporary index: %w", err)
	}
	if err := buildIndex(db, entries); err != nil {
		db.Close()
		return 0, err
	}
	if err := db.Close(); err != nil {
		return 0, fmt.Errorf("close temporary index: %w", err)
	}
	indexPath := filepath.Join(indexDir, "archive.db")
	if err := os.Rename(temporaryPath, indexPath); err != nil {
		return 0, fmt.Errorf("install rebuilt index: %w", err)
	}
	if err := s.ensureEmbeddings(entries); err != nil {
		return 0, fmt.Errorf("index rebuilt but embeddings failed (fix the embeddings backend, then run archive reindex): %w", err)
	}
	return len(entries), nil
}

const indexSchemaVersion = 1

func buildIndex(db *sql.DB, entries []Entry) error {
	statements := []string{
		fmt.Sprintf(`PRAGMA user_version = %d`, indexSchemaVersion),
		`PRAGMA journal_mode=DELETE`,
		`CREATE TABLE entries (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			category TEXT NOT NULL,
			tags TEXT NOT NULL,
			body TEXT NOT NULL
		)`,
		`CREATE VIRTUAL TABLE entries_fts USING fts5(
			id UNINDEXED,
			title,
			tags,
			body,
			tokenize='porter unicode61'
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("initialize search index: %w", err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin index transaction: %w", err)
	}
	defer tx.Rollback()
	metadata, err := tx.Prepare(`INSERT INTO entries (id, title, category, tags, body) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare metadata insert: %w", err)
	}
	defer metadata.Close()
	fts, err := tx.Prepare(`INSERT INTO entries_fts (id, title, tags, body) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare search insert: %w", err)
	}
	defer fts.Close()
	for _, entry := range entries {
		tags := strings.Join(entry.Tags, " ")
		if _, err := metadata.Exec(entry.ID, entry.Title, entry.Category, tags, entry.Body); err != nil {
			return fmt.Errorf("index metadata for %q: %w", entry.ID, err)
		}
		if _, err := fts.Exec(entry.ID, entry.Title, tags, entry.Body); err != nil {
			return fmt.Errorf("index text for %q: %w", entry.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit search index: %w", err)
	}
	return nil
}

type SearchMode int

const (
	ModeAuto SearchMode = iota
	ModeLexical
	ModeSemantic
)

const fusionDepth = 50

func (s *Store) Search(query, category string, limit int, mode SearchMode) ([]SearchResult, error) {
	if err := s.requireInitialized(); err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, errors.New("search limit must be at least 1")
	}
	if mode == ModeSemantic {
		return s.searchSemantic(query, category, limit)
	}
	depth := limit
	if mode == ModeAuto && s.Embedder != nil && depth < fusionDepth {
		depth = fusionDepth
	}
	lexical, err := s.searchLexical(query, category, depth)
	if err != nil {
		return nil, err
	}
	if mode == ModeLexical || s.Embedder == nil {
		if len(lexical) > limit {
			lexical = lexical[:limit]
		}
		return lexical, nil
	}
	semantic, err := s.searchSemantic(query, category, fusionDepth)
	if err != nil {
		return nil, err
	}
	return fuseRRF(lexical, semantic, limit), nil
}

func (s *Store) searchLexical(query, category string, limit int) ([]SearchResult, error) {
	match, err := ftsQuery(query)
	if err != nil {
		return nil, err
	}
	indexPath := filepath.Join(s.Root, ".index", "archive.db")
	db, err := s.openCurrentIndex(indexPath)
	if err != nil {
		return nil, err
	}
	if db == nil {
		if _, err := s.Reindex(); err != nil {
			return nil, fmt.Errorf("rebuild search index: %w", err)
		}
		db, err = s.openCurrentIndex(indexPath)
		if err != nil {
			return nil, err
		}
		if db == nil {
			return nil, errors.New("search index is still missing or stale after rebuild")
		}
	}
	defer db.Close()

	statement := `
		SELECT e.id, e.title, e.category, e.tags,
		       bm25(entries_fts, 0.0, 5.0, 5.0, 1.0) AS score,
		       snippet(entries_fts, -1, '[', ']', '...', 18) AS match_snippet
		FROM entries_fts
		JOIN entries e ON e.id = entries_fts.id
		WHERE entries_fts MATCH ?`
	args := []any{match}
	if category != "" {
		statement += ` AND e.category = ?`
		args = append(args, category)
	}
	statement += ` ORDER BY score ASC, e.id ASC LIMIT ?`
	args = append(args, limit)
	rows, err := db.Query(statement, args...)
	if err != nil {
		return nil, fmt.Errorf("search index: %w", err)
	}
	defer rows.Close()
	results := []SearchResult{}
	for rows.Next() {
		var result SearchResult
		var tags string
		if err := rows.Scan(&result.ID, &result.Title, &result.Category, &tags, &result.Score, &result.Snippet); err != nil {
			return nil, fmt.Errorf("read search result: %w", err)
		}
		result.Rank = len(results) + 1
		result.Snippet = strings.TrimSpace(result.Snippet)
		if tags != "" {
			result.Tags = strings.Fields(tags)
		} else {
			result.Tags = []string{}
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read search results: %w", err)
	}
	return results, nil
}

// openCurrentIndex returns (nil, nil) when the index is missing, unreadable,
// or built for another schema version; the caller rebuilds it. The index is a
// disposable cache, so rebuilding is the remediation for every stale state.
func (s *Store) openCurrentIndex(indexPath string) (*sql.DB, error) {
	if _, err := os.Stat(indexPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("inspect search index: %w", err)
	}
	dsn := (&url.URL{Scheme: "file", Path: indexPath, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open search index: %w", err)
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != indexSchemaVersion {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("close stale search index: %w", closeErr)
		}
		return nil, nil
	}
	return db, nil
}

func ftsQuery(query string) (string, error) {
	terms := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(terms) == 0 {
		return "", errors.New("search query must contain at least one letter or number")
	}
	quoted := make([]string, 0, len(terms))
	seen := map[string]bool{}
	for _, term := range terms {
		if !seen[term] {
			seen[term] = true
			quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
		}
	}
	return strings.Join(quoted, " OR "), nil
}
