package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkTextTail(b *testing.B) {
	for _, lines := range []int{10, 1000, 10000} {
		b.Run(fmt.Sprintf("lines=%d", lines), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "transcript.jsonl")
			line := `{"type":"assistant","message":{"role":"assistant","content":"` + strings.Repeat("x", 200) + `"}}` + "\n"
			if err := os.WriteFile(path, []byte(strings.Repeat(line, lines)), 0o644); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(line) * lines))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := Text(path, 40*1024); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
