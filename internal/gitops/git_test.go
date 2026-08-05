package gitops

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasConflictMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conflicted.txt")
	content := "before\n<<<<<<< HEAD\nlocal\n=======\nremote\n>>>>>>> origin/main\nafter\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	hasMarkers, err := hasConflictMarkers(path)
	if err != nil {
		t.Fatal(err)
	}
	if !hasMarkers {
		t.Fatal("expected conflict markers to be detected")
	}
}

func TestHasConflictMarkersIgnoresPartialMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	content := "documented marker: <<<<<<< HEAD\nwithout the rest of a conflict block\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	hasMarkers, err := hasConflictMarkers(path)
	if err != nil {
		t.Fatal(err)
	}
	if hasMarkers {
		t.Fatal("did not expect partial markers to be treated as a conflict")
	}
}
