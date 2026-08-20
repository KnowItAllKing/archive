package archive

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

type Status struct {
	Store       string         `json:"store"`
	Format      int            `json:"format"`
	Entries     int            `json:"entries"`
	Categories  map[string]int `json:"categories"`
	Jots        int            `json:"jots"`
	NeedsReview int            `json:"needs_review"`
	Embeddings  string         `json:"embeddings"`
	Embedded    int            `json:"embedded"`
	Remote      string         `json:"remote,omitempty"`
	Unpushed    int            `json:"unpushed"`
	Dirty       []string       `json:"dirty"`
}

func (s *Store) Status() (Status, error) {
	if err := s.requireInitialized(); err != nil {
		return Status{}, err
	}
	format, err := s.formatVersion()
	if err != nil {
		return Status{}, err
	}
	entries, err := s.readAllEntries()
	if err != nil {
		return Status{}, err
	}
	categories := map[string]int{}
	jots := 0
	needsReview := 0
	today := s.now().Format("2006-01-02")
	for _, entry := range entries {
		categories[entry.Category]++
		if contains(entry.Tags, "jot") {
			jots++
		}
		if reviewDue(entry, today) {
			needsReview++
		}
	}
	embeddings := "off"
	embedded := 0
	if s.Embedder != nil {
		embeddings = s.Embedder.ID()
		embedded, err = s.countEmbedded(entries)
		if err != nil {
			return Status{}, err
		}
	}
	remote, err := s.remoteURL()
	if err != nil {
		return Status{}, err
	}
	unpushed := 0
	if remote != "" {
		unpushed, err = s.unpushedCount()
		if err != nil {
			return Status{}, err
		}
	}
	porcelain, err := s.gitOutput("status", "--porcelain")
	if err != nil {
		return Status{}, err
	}
	dirty := []string{}
	for _, line := range strings.Split(strings.TrimRight(porcelain, "\n"), "\n") {
		if line != "" {
			dirty = append(dirty, strings.TrimSpace(line))
		}
	}
	return Status{
		Store: s.Root, Format: format, Entries: len(entries), Categories: categories,
		Jots: jots, NeedsReview: needsReview,
		Embeddings: embeddings, Embedded: embedded,
		Remote: remote, Unpushed: unpushed, Dirty: dirty,
	}, nil
}

// Sync reconciles changes made outside the CLI: it validates every entry,
// rebuilds the derived index, commits hand edits, and reports the backlog.
// Validation runs first so a broken hand edit is never committed.
func (s *Store) Sync(output io.Writer) error {
	if err := s.requireInitialized(); err != nil {
		return err
	}
	if _, err := s.readAllEntries(); err != nil {
		return fmt.Errorf("store validation failed; fix the hand edit, then rerun archive sync: %w", err)
	}
	count, err := s.Reindex()
	if err != nil {
		return err
	}
	porcelain, err := s.gitOutput("status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(porcelain) != "" {
		if err := s.commitAll("sync: hand edits"); err != nil {
			return err
		}
		fmt.Fprintln(output, "committed hand edits")
	}
	status, err := s.Status()
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "synced %d entries\n", count)
	if status.Jots > 0 {
		fmt.Fprintf(output, "jot backlog: %d (archive list --tag jot)\n", status.Jots)
	}
	if inbox := status.Categories["inbox"]; inbox > 0 {
		fmt.Fprintf(output, "inbox: %d awaiting categorization\n", inbox)
	}
	if status.NeedsReview > 0 {
		fmt.Fprintf(output, "needs review: %d (archive list --due-review)\n", status.NeedsReview)
	}
	if status.Unpushed > 0 {
		fmt.Fprintf(output, "unpushed: %d commits (archive push)\n", status.Unpushed)
	}
	return nil
}

func (s *Store) Push(output io.Writer) error {
	if err := s.requireInitialized(); err != nil {
		return err
	}
	remote, err := s.remoteURL()
	if err != nil {
		return err
	}
	if remote == "" {
		return errors.New("no remote configured; use archive init --remote URL for a new store, or git -C <store> remote add origin URL")
	}
	if err := s.git("push", "--quiet", "-u", "origin", "HEAD"); err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "pushed to %s\n", remote)
	return err
}
