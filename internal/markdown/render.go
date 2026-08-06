// Package markdown renders GitHub-flavored comment text for the terminal.
package markdown

import (
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
)

var renderCache = struct {
	sync.Mutex
	items map[string]string
}{items: map[string]string{}}

// Render formats Markdown. Glamour renders image links as their source URL;
// ordinary video URLs remain unchanged.
func Render(text string, width int) string {
	if width < 20 {
		width = 20
	}
	key := fmt.Sprintf("%d\x00%s", width, text)
	renderCache.Lock()
	if out, ok := renderCache.items[key]; ok {
		renderCache.Unlock()
		return out
	}
	renderCache.Unlock()

	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	out = strings.TrimSpace(out)

	renderCache.Lock()
	if len(renderCache.items) >= 512 {
		clear(renderCache.items)
	}
	renderCache.items[key] = out
	renderCache.Unlock()
	return out
}
