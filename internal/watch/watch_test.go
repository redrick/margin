package watch

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestWatcherReportsAllChangedPaths(t *testing.T) {
	dir := t.TempDir()
	content, reader := filepath.Join(dir, "review"), filepath.Join(dir, "reader")
	events := make(chan []string, 4)
	w, err := NewWithPaths(func(paths []string) { events <- paths })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Set([]string{content, reader})
	for _, path := range []string{content, reader} {
		if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case paths := <-events:
		if len(paths) != 2 || !slices.Contains(paths, content) || !slices.Contains(paths, reader) {
			t.Fatalf("changed paths = %v, want both files", paths)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no change notification")
	}
}
