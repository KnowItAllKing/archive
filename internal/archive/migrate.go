package archive

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const storeFormatVersion = 1

type storeManifest struct {
	Format int `yaml:"format"`
}

type migration struct {
	to    int
	title string
	apply func(s *Store) error
}

// Steps upgrade a store one format version at a time and must stay in order.
var migrations = []migration{}

func (s *Store) formatVersion() (int, error) {
	data, err := os.ReadFile(s.manifestPath())
	if errors.Is(err, os.ErrNotExist) {
		// Stores created before the manifest existed are format 1.
		return 1, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read store.yaml: %w", err)
	}
	var manifest storeManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return 0, fmt.Errorf("parse store.yaml: %w", err)
	}
	if manifest.Format < 1 {
		return 0, fmt.Errorf("store.yaml declares invalid format %d", manifest.Format)
	}
	return manifest.Format, nil
}

func (s *Store) writeFormatVersion(version int) error {
	data := fmt.Sprintf("format: %d\n", version)
	if err := os.WriteFile(s.manifestPath(), []byte(data), 0o644); err != nil {
		return fmt.Errorf("write store.yaml: %w", err)
	}
	return nil
}

func (s *Store) manifestPath() string {
	return filepath.Join(s.Root, "store.yaml")
}

func (s *Store) checkFormat() error {
	version, err := s.formatVersion()
	if err != nil {
		return err
	}
	if version > storeFormatVersion {
		return fmt.Errorf("store format %d is newer than this binary supports (format %d); upgrade archive", version, storeFormatVersion)
	}
	if version < storeFormatVersion {
		return fmt.Errorf("store format %d is behind the current format %d; run archive migrate", version, storeFormatVersion)
	}
	return nil
}

func (s *Store) Migrate(output io.Writer) error {
	if err := s.requireGit(); err != nil {
		return err
	}
	version, err := s.formatVersion()
	if err != nil {
		return err
	}
	if version > storeFormatVersion {
		return fmt.Errorf("store format %d is newer than this binary supports (format %d); upgrade archive", version, storeFormatVersion)
	}
	if version == storeFormatVersion {
		if _, err := os.Stat(s.manifestPath()); errors.Is(err, os.ErrNotExist) {
			if err := s.writeFormatVersion(version); err != nil {
				return err
			}
			if err := s.commitAll(fmt.Sprintf("migrate: stamp format %d", version)); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintf(output, "store is already at format %d\n", version)
		return err
	}
	for _, step := range migrations {
		if step.to <= version {
			continue
		}
		if err := step.apply(s); err != nil {
			return fmt.Errorf("migrate to format %d (%s): %w", step.to, step.title, err)
		}
		if err := s.writeFormatVersion(step.to); err != nil {
			return err
		}
		if err := s.commitAll(fmt.Sprintf("migrate: format %d -> %d (%s)", version, step.to, step.title)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "migrated store to format %d: %s\n", step.to, step.title); err != nil {
			return err
		}
		version = step.to
	}
	if _, err := s.Reindex(); err != nil {
		return fmt.Errorf("store migrated but index rebuild failed: %w", err)
	}
	return nil
}
