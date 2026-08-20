package archive

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "store"))
	store.now = func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}
	if err := store.Init(&bytes.Buffer{}, ""); err != nil {
		t.Fatal(err)
	}
	return store
}

func addTestEntry(t *testing.T, store *Store) Entry {
	t.Helper()
	entry, err := store.Add(AddInput{
		Title: "Keycloak client scope", Category: "infra",
		Tags: []string{"keycloak", "auth"}, Source: "session:test",
		Body: "The GCIP scope question.\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestInitWritesFormatManifest(t *testing.T) {
	store := newTestStore(t)
	data, err := os.ReadFile(store.manifestPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "format: 1\n" {
		t.Fatalf("store.yaml = %q", data)
	}
}

func TestSearchRebuildsStaleIndex(t *testing.T) {
	store := newTestStore(t)
	entry := addTestEntry(t, store)

	indexPath := filepath.Join(store.Root, ".index", "archive.db")
	db, err := sql.Open("sqlite", indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	results, err := store.Search("keycloak", "", 10, ModeAuto)
	if err != nil {
		t.Fatalf("search did not rebuild stale index: %v", err)
	}
	if len(results) != 1 || results[0].ID != entry.ID {
		t.Fatalf("results after rebuild = %#v", results)
	}
}

func TestMigrateAlreadyCurrent(t *testing.T) {
	store := newTestStore(t)
	var output bytes.Buffer
	if err := store.Migrate(&output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "already at format 1") {
		t.Fatalf("migrate output = %q", output.String())
	}
}

func TestMigrateStampsPreManifestStore(t *testing.T) {
	store := newTestStore(t)
	if err := os.Remove(store.manifestPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List("", ""); err != nil {
		t.Fatalf("pre-manifest store rejected: %v", err)
	}
	if err := store.Migrate(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.manifestPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "format: 1\n" {
		t.Fatalf("store.yaml = %q", data)
	}
	logOutput := gitOutput(t, store.Root, "log", "-1", "--format=%s")
	if strings.TrimSpace(logOutput) != "migrate: stamp format 1" {
		t.Fatalf("latest commit = %q", logOutput)
	}
}

func TestFormatVersionGates(t *testing.T) {
	store := newTestStore(t)
	if err := os.WriteFile(store.manifestPath(), []byte("format: 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List("", ""); err == nil || !strings.Contains(err.Error(), "newer than this binary") {
		t.Fatalf("newer-format error = %v", err)
	}
	if err := store.Migrate(&bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "newer than this binary") {
		t.Fatalf("migrate newer-format error = %v", err)
	}
	if err := os.WriteFile(store.manifestPath(), []byte("format: 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.List("", ""); err == nil || !strings.Contains(err.Error(), "invalid format") {
		t.Fatalf("invalid-format error = %v", err)
	}
}

func TestAddRejectsEmptyBody(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Add(AddInput{Title: "Empty", Category: "inbox", Body: "\n\n"})
	if err == nil || !strings.Contains(err.Error(), "body is empty") {
		t.Fatalf("empty body error = %v", err)
	}
}

func TestUpdateBodyGuards(t *testing.T) {
	store := newTestStore(t)
	entry := addTestEntry(t, store)

	if _, err := store.Update(entry.ID, UpdateInput{}); err == nil || !strings.Contains(err.Error(), "--keep-body") {
		t.Fatalf("empty replacement body error = %v", err)
	}
	if _, err := store.Update(entry.ID, UpdateInput{KeepBody: true}); err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("no-op update error = %v", err)
	}
	if _, err := store.Update(entry.ID, UpdateInput{KeepBody: true, Body: "new\n"}); err == nil || !strings.Contains(err.Error(), "cannot combine") {
		t.Fatalf("keep-body with body error = %v", err)
	}

	updated, err := store.Update(entry.ID, UpdateInput{KeepBody: true, Tags: []string{"keycloak", "gcip"}, SetTags: true})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Body != entry.Body {
		t.Fatalf("keep-body changed body from %q to %q", entry.Body, updated.Body)
	}
	if len(updated.Tags) != 2 {
		t.Fatalf("tags = %v", updated.Tags)
	}
}

func TestAddRecreatesMissingRawDirectory(t *testing.T) {
	store := newTestStore(t)
	if err := os.RemoveAll(filepath.Join(store.Root, "raw")); err != nil {
		t.Fatal(err)
	}
	rawSource := filepath.Join(t.TempDir(), "conversation.txt")
	if err := os.WriteFile(rawSource, []byte("verbatim\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry, err := store.Add(AddInput{
		Title: "Fresh clone raw stash", Category: "inbox",
		Body: "Distilled.\n", RawFile: rawSource,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, filepath.FromSlash(entry.Raw))); err != nil {
		t.Fatal(err)
	}
}
