package markdown

import (
	"fmt"
	"testing"
)

func BenchmarkRender(b *testing.B) {
	const text = "# Heading\n\nA paragraph with **bold** text and `code`."
	b.Run("cache-hit", func(b *testing.B) {
		Render(text, 80)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_ = Render(text, 80)
		}
	})
	b.Run("cache-miss", func(b *testing.B) {
		b.ReportAllocs()
		for i := range b.N {
			_ = Render(fmt.Sprintf("%s\n\n%d", text, i), 80)
		}
	})
}
