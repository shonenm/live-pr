package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func BenchmarkChangeDetection(b *testing.B) {
	for _, files := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("files=%d", files), func(b *testing.B) {
			dir := b.TempDir()
			run := func(args ...string) {
				b.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					b.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			run("init", "-b", "main")
			run("config", "user.name", "Benchmark")
			run("config", "user.email", "benchmark@example.com")
			if err := os.WriteFile(filepath.Join(dir, "base"), []byte("base\n"), 0o644); err != nil {
				b.Fatal(err)
			}
			run("add", ".")
			run("commit", "-m", "base")
			run("switch", "-c", "feature")
			for i := range files {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%04d", i)), []byte("change\n"), 0o644); err != nil {
					b.Fatal(err)
				}
			}
			run("add", ".")
			run("commit", "-m", "changes")
			old, err := os.Getwd()
			if err != nil {
				b.Fatal(err)
			}
			if err := os.Chdir(dir); err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = os.Chdir(old) })

			b.Run("quiet", func(b *testing.B) {
				for range b.N {
					if _, err := HasChanges("main", "HEAD"); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("enumerate", func(b *testing.B) {
				for range b.N {
					if _, err := ChangedFilesRange("main", "HEAD"); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run("worktree", func(b *testing.B) {
				for range b.N {
					if _, err := HasUncommittedChanges(); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
