package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceFailurePreservesDestination(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("unsent data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Replace(filepath.Join(dir, "missing"), target); err == nil {
		t.Fatal("missing source succeeded")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "unsent data" {
		t.Fatalf("old data lost: %q %v", got, err)
	}
}

func TestReplaceNewFile(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "source"), filepath.Join(dir, "target")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Replace(source, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new" {
		t.Fatalf("new target = %q %v", got, err)
	}
}

func TestReplaceExistingFile(t *testing.T) {
	dir := t.TempDir()
	target, tmp := filepath.Join(dir, "target"), filepath.Join(dir, "tmp")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Replace(tmp, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new" {
		t.Fatalf("target = %q err=%v", got, err)
	}
}
