package archive

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusReportsStoreState(t *testing.T) {
	store := newTestStore(t)
	addTestEntry(t, store)

	status, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Format != 1 || status.Entries != 1 || status.Categories["infra"] != 1 {
		t.Fatalf("status = %#v", status)
	}
	if status.Remote != "" || status.Unpushed != 0 {
		t.Fatalf("remote state = %#v", status)
	}
	if len(status.Dirty) != 0 {
		t.Fatalf("dirty = %v", status.Dirty)
	}

	if err := os.WriteFile(filepath.Join(store.Root, "stray.txt"), []byte("edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err = store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Dirty) != 1 || !strings.Contains(status.Dirty[0], "stray.txt") {
		t.Fatalf("dirty = %v", status.Dirty)
	}
}

func TestInitWithRemoteTracksUnpushed(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "store"))
	store.now = func() time.Time {
		return time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	}
	var output bytes.Buffer
	if err := store.Init(&output, "https://example.invalid/store.git"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "remote: https://example.invalid/store.git") {
		t.Fatalf("init output = %q", output.String())
	}

	status, err := store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Remote != "https://example.invalid/store.git" {
		t.Fatalf("remote = %q", status.Remote)
	}
	if status.Unpushed != 1 {
		t.Fatalf("unpushed after init = %d", status.Unpushed)
	}

	addTestEntry(t, store)
	status, err = store.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.Unpushed != 2 {
		t.Fatalf("unpushed after add = %d", status.Unpushed)
	}
}

func TestPushRequiresRemote(t *testing.T) {
	store := newTestStore(t)
	err := store.Push(&bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no remote configured") {
		t.Fatalf("push error = %v", err)
	}
}
