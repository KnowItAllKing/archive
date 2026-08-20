package archive

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStoreWorkflow(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "archive store"))
	store.now = func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}
	var initOutput bytes.Buffer
	if err := store.Init(&initOutput, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(initOutput.String(), store.Root) {
		t.Fatalf("init output does not name store: %q", initOutput.String())
	}

	keycloak, err := store.Add(AddInput{
		Title:    "Keycloak gcip client scope on PFI ingest required or not",
		Category: "infra",
		Tags:     []string{"keycloak", "auth", "terraform", "client-secret"},
		Source:   "session:test",
		Body:     "PFI ingest uses a Keycloak client. Terraform supplies its secret.\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []AddInput{
		{Title: "Go testing notes", Category: "dev", Tags: []string{"go", "tests"}, Source: "session:test", Body: "Table tests and temporary directories.\n"},
		{Title: "General authentication reference", Category: "reference", Tags: []string{"auth", "security"}, Source: "session:test", Body: "Browser sessions use login cookies.\n"},
	} {
		if _, err := store.Add(input); err != nil {
			t.Fatal(err)
		}
	}

	before, err := store.Search("client secret keycloak auth", "", 10, ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) < 2 || before[0].ID != keycloak.ID {
		t.Fatalf("keycloak entry was not first: %#v", before)
	}
	if strings.HasPrefix(before[0].Snippet, "\n") {
		t.Fatalf("snippet has leading blank line: %q", before[0].Snippet)
	}

	store.now = func() time.Time {
		return time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	}
	updated, err := store.Update(keycloak.ID, UpdateInput{
		Body:      "PFI ingest requires the GCIP Keycloak client scope and a Terraform-managed secret.\n",
		Tags:      []string{"keycloak", "auth", "terraform", "gcip", "client-secret"},
		SetTags:   true,
		Source:    "session:follow-up",
		SetSource: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Updated != "2026-08-21" {
		t.Fatalf("updated date = %q", updated.Updated)
	}

	indexPath := filepath.Join(store.Root, ".index", "archive.db")
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Search("client secret keycloak auth", "", 10, ModeAuto); err != nil {
		t.Fatalf("search did not rebuild missing index: %v", err)
	}
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("index was not rebuilt on disk: %v", err)
	}
	if count, err := store.Reindex(); err != nil {
		t.Fatal(err)
	} else if count != 3 {
		t.Fatalf("reindexed %d entries, want 3", count)
	}
	after, err := store.Search("client secret keycloak auth", "", 10, ModeAuto)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeIDs(before), beforeIDs(after)) {
		t.Fatalf("result IDs changed after rebuild: before=%v after=%v", beforeIDs(before), beforeIDs(after))
	}

	logOutput := gitOutput(t, store.Root, "log", "--format=%s")
	lines := strings.Split(strings.TrimSpace(logOutput), "\n")
	if len(lines) != 5 {
		t.Fatalf("git log has %d commits, want 5:\n%s", len(lines), logOutput)
	}
	if lines[0] != "update: "+keycloak.ID {
		t.Fatalf("latest commit = %q", lines[0])
	}
	if tracked := strings.TrimSpace(gitOutput(t, store.Root, "ls-files", ".index/archive.db")); tracked != "" {
		t.Fatalf("derived index is tracked: %q", tracked)
	}
}

func TestAddRawInboxEntry(t *testing.T) {
	root := t.TempDir()
	rawSource := filepath.Join(root, "conversation.txt")
	if err := os.WriteFile(rawSource, []byte("verbatim conversation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(filepath.Join(root, "store"))
	store.now = func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}
	if err := store.Init(&bytes.Buffer{}, ""); err != nil {
		t.Fatal(err)
	}
	entry, err := store.Add(AddInput{
		Title: "Résumé notes", Category: "inbox", Tags: []string{"résumé"},
		Source: "session:test", Body: "Canonical conclusion.\n", RawFile: rawSource,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(entry.ID, "résumé-notes") {
		t.Fatalf("unicode slug = %q", entry.ID)
	}
	if entry.Raw != "raw/"+entry.ID+".md" {
		t.Fatalf("raw path = %q", entry.Raw)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "inbox", entry.ID+".md")); err != nil {
		t.Fatal(err)
	}
	rawData, err := os.ReadFile(filepath.Join(store.Root, filepath.FromSlash(entry.Raw)))
	if err != nil {
		t.Fatal(err)
	}
	if string(rawData) != "verbatim conversation\n" {
		t.Fatalf("raw data = %q", rawData)
	}
}

func TestInitRefusesNonEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(root)
	if err := store.Init(&bytes.Buffer{}, ""); err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("Init error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "keep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("existing file changed to %q", data)
	}
}

func beforeIDs(results []SearchResult) []string {
	ids := make([]string, len(results))
	for i, result := range results {
		ids[i] = result.ID
	}
	return ids
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	gitArgs := append([]string{"-c", "core.fsmonitor=false"}, args...)
	command := exec.Command("git", gitArgs...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
