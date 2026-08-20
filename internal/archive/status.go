package archive

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

type Status struct {
	Store      string         `json:"store"`
	Format     int            `json:"format"`
	Entries    int            `json:"entries"`
	Categories map[string]int `json:"categories"`
	Embeddings string         `json:"embeddings"`
	Embedded   int            `json:"embedded"`
	Remote     string         `json:"remote,omitempty"`
	Unpushed   int            `json:"unpushed"`
	Dirty      []string       `json:"dirty"`
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
	for _, entry := range entries {
		categories[entry.Category]++
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
		Embeddings: embeddings, Embedded: embedded,
		Remote: remote, Unpushed: unpushed, Dirty: dirty,
	}, nil
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
