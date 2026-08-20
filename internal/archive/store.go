package archive

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const starterCategories = `infra: Infrastructure, deployment, networking, and operations.
dev: Software design, implementation, debugging, and tools.
ai: Models, agents, prompts, and AI systems.
decisions: Decisions, their context, and their consequences.
reference: Facts and procedures worth looking up again.
personal: Personal preferences, plans, and notes.
`

type Store struct {
	Root     string
	Embedder Embedder
	now      func() time.Time
}

type AddInput struct {
	Title    string
	Category string
	Tags     []string
	Source   string
	Body     string
	RawFile  string
}

type UpdateInput struct {
	Body        string
	KeepBody    bool
	Title       string
	SetTitle    bool
	Category    string
	SetCategory bool
	Tags        []string
	SetTags     bool
	Source      string
	SetSource   bool
}

type Category struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func DefaultStore() (*Store, error) {
	root := os.Getenv("ARCHIVE_STORE")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("find home directory for default ARCHIVE_STORE: %w", err)
		}
		root = filepath.Join(home, "archive-store")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve ARCHIVE_STORE %q: %w", root, err)
	}
	embedder, err := NewEmbedderFromEnv()
	if err != nil {
		return nil, err
	}
	return &Store{Root: abs, Embedder: embedder, now: time.Now}, nil
}

func NewStore(root string) *Store {
	return &Store{Root: root, now: time.Now}
}

func (s *Store) Init(output io.Writer, remote string) error {
	if _, err := os.Stat(filepath.Join(s.Root, ".git")); err == nil {
		return fmt.Errorf("store %q is already initialized", s.Root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect store %q: %w", s.Root, err)
	}
	if items, err := os.ReadDir(s.Root); err == nil && len(items) > 0 {
		return fmt.Errorf("refusing to initialize non-empty directory %q", s.Root)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect store directory %q: %w", s.Root, err)
	}
	for _, dir := range []string{
		s.Root,
		filepath.Join(s.Root, "entries"),
		filepath.Join(s.Root, "inbox"),
		filepath.Join(s.Root, "raw"),
		filepath.Join(s.Root, ".index"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create store directory %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.Root, "categories.yaml"), []byte(starterCategories), 0o644); err != nil {
		return fmt.Errorf("write categories.yaml: %w", err)
	}
	if err := s.writeFormatVersion(storeFormatVersion); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.Root, ".gitignore"), []byte(".index/\n"), 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	if err := s.git("init", "--quiet"); err != nil {
		return err
	}
	if _, err := s.Reindex(); err != nil {
		return err
	}
	if err := s.commit("init archive store", ".gitignore", "categories.yaml", "store.yaml"); err != nil {
		return err
	}
	if remote != "" {
		if err := s.git("remote", "add", "origin", remote); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(output, "initialized archive store at %s\n", s.Root); err != nil {
		return err
	}
	if remote != "" {
		if _, err := fmt.Fprintf(output, "remote: %s\n", remote); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Add(input AddInput) (Entry, error) {
	if err := s.requireInitialized(); err != nil {
		return Entry{}, err
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		return Entry{}, errors.New("title must not be empty")
	}
	if err := s.validateCategory(input.Category); err != nil {
		return Entry{}, err
	}
	tags, err := normalizeTags(input.Tags)
	if err != nil {
		return Entry{}, err
	}
	if normalizeBody(input.Body) == "" {
		return Entry{}, errors.New("distilled body is empty; pipe it on stdin or pass --file")
	}
	date := s.now().Format("2006-01-02")
	base := slugify(input.Title)
	if base == "" {
		return Entry{}, errors.New("title must contain at least one letter or number")
	}
	id, err := s.uniqueID(date + "-" + base)
	if err != nil {
		return Entry{}, err
	}
	entry := Entry{
		ID: id, Title: input.Title, Category: input.Category, Tags: tags,
		Created: date, Updated: date, Source: strings.TrimSpace(input.Source), Body: normalizeBody(input.Body),
	}
	paths := []string{filepath.ToSlash(s.relativeEntryPath(entry))}
	if input.RawFile != "" {
		rawData, err := os.ReadFile(input.RawFile)
		if err != nil {
			return Entry{}, fmt.Errorf("read raw source %q: %w", input.RawFile, err)
		}
		entry.Raw = filepath.ToSlash(filepath.Join("raw", id+".md"))
		rawPath := filepath.Join(s.Root, filepath.FromSlash(entry.Raw))
		if err := os.MkdirAll(filepath.Dir(rawPath), 0o755); err != nil {
			return Entry{}, fmt.Errorf("create raw directory: %w", err)
		}
		if err := os.WriteFile(rawPath, rawData, 0o644); err != nil {
			return Entry{}, fmt.Errorf("stash raw source: %w", err)
		}
		paths = append(paths, entry.Raw)
	}
	if err := s.writeEntry(entry); err != nil {
		return Entry{}, err
	}
	if _, err := s.Reindex(); err != nil {
		return Entry{}, fmt.Errorf("entry was written but index rebuild failed: %w", err)
	}
	if err := s.commit("add: "+entry.ID, paths...); err != nil {
		return Entry{}, fmt.Errorf("entry was written but git commit failed: %w", err)
	}
	return entry, nil
}

func (s *Store) Update(id string, input UpdateInput) (Entry, error) {
	if err := s.requireInitialized(); err != nil {
		return Entry{}, err
	}
	entry, _, oldPath, err := s.findEntry(id)
	if err != nil {
		return Entry{}, err
	}
	if input.SetTitle {
		if strings.TrimSpace(input.Title) == "" {
			return Entry{}, errors.New("title must not be empty")
		}
		entry.Title = strings.TrimSpace(input.Title)
	}
	if input.SetCategory {
		if err := s.validateCategory(input.Category); err != nil {
			return Entry{}, err
		}
		entry.Category = input.Category
	}
	if input.SetTags {
		entry.Tags, err = normalizeTags(input.Tags)
		if err != nil {
			return Entry{}, err
		}
	}
	if input.SetSource {
		entry.Source = strings.TrimSpace(input.Source)
	}
	body := normalizeBody(input.Body)
	if input.KeepBody {
		if body != "" {
			return Entry{}, errors.New("cannot combine --keep-body with a replacement body")
		}
		if !input.SetTitle && !input.SetCategory && !input.SetTags && !input.SetSource {
			return Entry{}, errors.New("nothing to update: --keep-body was set and no fields were changed")
		}
	} else if body == "" {
		return Entry{}, errors.New("replacement body is empty; pipe the new distillate, use --file, or pass --keep-body to keep the existing body")
	} else {
		entry.Body = body
	}
	entry.Updated = s.now().Format("2006-01-02")
	newPath := s.entryPath(entry)
	if err := s.writeEntry(entry); err != nil {
		return Entry{}, err
	}
	paths := []string{filepath.ToSlash(s.relativeEntryPath(entry))}
	if oldPath != newPath {
		if err := os.Remove(oldPath); err != nil {
			return Entry{}, fmt.Errorf("move entry from old category: %w", err)
		}
		oldRelative, err := filepath.Rel(s.Root, oldPath)
		if err != nil {
			return Entry{}, fmt.Errorf("resolve old entry path: %w", err)
		}
		paths = append(paths, filepath.ToSlash(oldRelative))
	}
	if _, err := s.Reindex(); err != nil {
		return Entry{}, fmt.Errorf("entry was updated but index rebuild failed: %w", err)
	}
	if err := s.commit("update: "+entry.ID, paths...); err != nil {
		return Entry{}, fmt.Errorf("entry was updated but git commit failed: %w", err)
	}
	return entry, nil
}

func (s *Store) Show(id string) (Entry, []byte, error) {
	if err := s.requireInitialized(); err != nil {
		return Entry{}, nil, err
	}
	entry, raw, _, err := s.findEntry(id)
	return entry, raw, err
}

func (s *Store) List(category, tag string) ([]Entry, error) {
	if err := s.requireInitialized(); err != nil {
		return nil, err
	}
	if tag != "" && !validTag.MatchString(tag) {
		return nil, fmt.Errorf("invalid tag %q: use lowercase letters, numbers, and hyphens", tag)
	}
	entries, err := s.readAllEntries()
	if err != nil {
		return nil, err
	}
	filtered := entries[:0]
	for _, entry := range entries {
		if category != "" && entry.Category != category {
			continue
		}
		if tag != "" && !contains(entry.Tags, tag) {
			continue
		}
		filtered = append(filtered, entry)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Updated == filtered[j].Updated {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].Updated > filtered[j].Updated
	})
	return filtered, nil
}

func (s *Store) Categories() ([]Category, error) {
	if err := s.requireInitialized(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.Root, "categories.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read categories.yaml: %w", err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("parse categories.yaml: %w", err)
	}
	if len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("categories.yaml must be a mapping of category names to descriptions")
	}
	root := node.Content[0]
	categories := make([]Category, 0, len(root.Content)/2)
	seen := map[string]bool{}
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Kind != yaml.ScalarNode || root.Content[i+1].Kind != yaml.ScalarNode {
			return nil, errors.New("categories.yaml must be a flat mapping of names to one-line descriptions")
		}
		name := root.Content[i].Value
		description := root.Content[i+1].Value
		if !validCategory.MatchString(name) || name == "inbox" {
			return nil, fmt.Errorf("categories.yaml contains invalid category %q", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("categories.yaml contains duplicate category %q", name)
		}
		seen[name] = true
		if strings.TrimSpace(description) == "" || strings.Contains(description, "\n") {
			return nil, fmt.Errorf("category %q must have a one-line description", name)
		}
		categories = append(categories, Category{Name: name, Description: description})
	}
	return categories, nil
}

func (s *Store) validateCategory(name string) error {
	if name == "inbox" {
		return nil
	}
	categories, err := s.Categories()
	if err != nil {
		return err
	}
	for _, category := range categories {
		if category.Name == name {
			return nil
		}
	}
	return fmt.Errorf("unknown category %q; run archive categories and choose a listed category, or use inbox", name)
}

func (s *Store) requireInitialized() error {
	if err := s.requireGit(); err != nil {
		return err
	}
	return s.checkFormat()
}

func (s *Store) requireGit() error {
	if _, err := os.Stat(filepath.Join(s.Root, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("archive store %q is not initialized; run archive init", s.Root)
		}
		return fmt.Errorf("inspect archive store %q: %w", s.Root, err)
	}
	return nil
}

func (s *Store) entryPath(entry Entry) string {
	return filepath.Join(s.Root, s.relativeEntryPath(entry))
}

func (s *Store) relativeEntryPath(entry Entry) string {
	if entry.Category == "inbox" {
		return filepath.Join("inbox", entry.ID+".md")
	}
	return filepath.Join("entries", entry.Category, entry.ID+".md")
}

func (s *Store) writeEntry(entry Entry) error {
	data, err := marshalEntry(entry)
	if err != nil {
		return err
	}
	path := s.entryPath(entry)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create category directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".entry-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary entry file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary entry file: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("set entry permissions: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary entry file: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("install entry file: %w", err)
	}
	return nil
}

func (s *Store) findEntry(id string) (Entry, []byte, string, error) {
	if !validID.MatchString(id) {
		return Entry{}, nil, "", fmt.Errorf("invalid entry id %q", id)
	}
	entries, err := s.entryFiles()
	if err != nil {
		return Entry{}, nil, "", err
	}
	for _, path := range entries {
		if strings.TrimSuffix(filepath.Base(path), ".md") != id {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return Entry{}, nil, "", fmt.Errorf("read entry %q: %w", id, err)
		}
		entry, err := parseEntry(data)
		if err != nil {
			return Entry{}, nil, "", fmt.Errorf("parse %s: %w", path, err)
		}
		if entry.ID != id {
			return Entry{}, nil, "", fmt.Errorf("entry filename identifies %q but frontmatter identifies %q", id, entry.ID)
		}
		if err := s.validateCategory(entry.Category); err != nil {
			return Entry{}, nil, "", fmt.Errorf("entry %q: %w", id, err)
		}
		if err := s.validateStoredPath(entry, path); err != nil {
			return Entry{}, nil, "", err
		}
		return entry, data, path, nil
	}
	return Entry{}, nil, "", fmt.Errorf("entry %q not found", id)
}

func (s *Store) readAllEntries() ([]Entry, error) {
	categories, err := s.Categories()
	if err != nil {
		return nil, err
	}
	allowedCategories := map[string]bool{"inbox": true}
	for _, category := range categories {
		allowedCategories[category.Name] = true
	}
	paths, err := s.entryFiles()
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(paths))
	seen := map[string]string{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		entry, err := parseEntry(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if previous, ok := seen[entry.ID]; ok {
			return nil, fmt.Errorf("duplicate entry id %q in %s and %s", entry.ID, previous, path)
		}
		seen[entry.ID] = path
		if !allowedCategories[entry.Category] {
			return nil, fmt.Errorf("entry %q uses category %q, which is absent from categories.yaml; use inbox or add the category to the vocabulary", entry.ID, entry.Category)
		}
		if err := s.validateStoredPath(entry, path); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *Store) validateStoredPath(entry Entry, path string) error {
	relative, err := filepath.Rel(s.Root, path)
	if err != nil {
		return fmt.Errorf("resolve entry path %s: %w", path, err)
	}
	expected := s.relativeEntryPath(entry)
	if filepath.Clean(relative) != filepath.Clean(expected) {
		return fmt.Errorf("entry %q is stored at %s, expected %s from its category", entry.ID, relative, expected)
	}
	if entry.Raw != "" {
		rawPath := filepath.Join(s.Root, filepath.FromSlash(entry.Raw))
		if _, err := os.Stat(rawPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("entry %q points to missing raw source %s", entry.ID, entry.Raw)
			}
			return fmt.Errorf("inspect raw source for entry %q: %w", entry.ID, err)
		}
	}
	return nil
}

func (s *Store) entryFiles() ([]string, error) {
	var paths []string
	for _, root := range []string{filepath.Join(s.Root, "entries"), filepath.Join(s.Root, "inbox")} {
		err := filepath.WalkDir(root, func(path string, item os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !item.IsDir() && strings.HasSuffix(item.Name(), ".md") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan entries under %s: %w", root, err)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func (s *Store) uniqueID(base string) (string, error) {
	paths, err := s.entryFiles()
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	for _, path := range paths {
		used[strings.TrimSuffix(filepath.Base(path), ".md")] = true
	}
	if !used[base] {
		return base, nil
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if !used[candidate] {
			return candidate, nil
		}
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
