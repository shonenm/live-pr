package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

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
