// Package markdown renders GitHub-flavored comment text for the terminal.
package markdown

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"

	"github.com/shonenm/live-pr/internal/theme"
)

type cacheKey struct {
	width int
	text  string
}

var renderCache = struct {
	sync.Mutex
	items map[cacheKey]string
}{items: map[cacheKey]string{}}

// renderers reuses one TermRenderer per wrap width: constructing one (style
// setup plus goldmark parser assembly) dominates cache-miss render cost.
// glamour's TermRenderer is not safe for concurrent Render calls (its ANSI
// node renderer mutates a shared block stack), so each entry carries its own
// lock. Only a handful of widths occur in practice, so the map is unbounded.
var renderers = struct {
	sync.Mutex
	items map[int]*widthRenderer
}{items: map[int]*widthRenderer{}}

type widthRenderer struct {
	sync.Mutex
	r *glamour.TermRenderer
}

func rendererFor(width int) (*widthRenderer, error) {
	renderers.Lock()
	defer renderers.Unlock()
	if wr, ok := renderers.items[width]; ok {
		return wr, nil
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(githubStyle()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil, err
	}
	wr := &widthRenderer{r: r}
	renderers.items[width] = wr
	return wr, nil
}

// Render formats Markdown. Glamour renders image links as their source URL;
// ordinary video URLs remain unchanged.
func Render(text string, width int) string {
	if width < 20 {
		width = 20
	}
	key := cacheKey{width: width, text: text}
	renderCache.Lock()
	if out, ok := renderCache.items[key]; ok {
		renderCache.Unlock()
		return out
	}
	renderCache.Unlock()

	wr, err := rendererFor(width)
	if err != nil {
		return text
	}
	wr.Lock()
	out, err := wr.r.Render(text)
	wr.Unlock()
	if err != nil {
		return text
	}
	out = strings.TrimSpace(out)

	renderCache.Lock()
	if len(renderCache.items) >= 512 {
		// Drop a single entry instead of clearing the whole cache, so one
		// overflow does not force a full re-render of every visible card.
		for k := range renderCache.items {
			delete(renderCache.items, k)
			break
		}
	}
	renderCache.items[key] = out
	renderCache.Unlock()
	return out
}

func githubStyle() glamouransi.StyleConfig {
	cfg := styles.DarkStyleConfig
	cfg.Document.StylePrimitive.Color = color(theme.Foreground)
	cfg.BlockQuote.StylePrimitive.Color = color(theme.Muted)
	cfg.Heading.StylePrimitive.Color = color(theme.Foreground)
	cfg.H1.StylePrimitive.Color = color(theme.Foreground)
	cfg.H1.StylePrimitive.BackgroundColor = nil
	cfg.H6.StylePrimitive.Color = color(theme.Muted)
	cfg.HorizontalRule.Color = color(theme.Border)
	cfg.Link.Color, cfg.LinkText.Color = color(theme.Accent), color(theme.Accent)
	cfg.Image.Color, cfg.ImageText.Color = color(theme.Accent), color(theme.Accent)
	cfg.Code.StylePrimitive.Color = color(theme.Foreground)
	cfg.Code.StylePrimitive.BackgroundColor = color(theme.BackgroundMuted)
	cfg.CodeBlock.StylePrimitive.Color = color(theme.Foreground)
	cfg.CodeBlock.StylePrimitive.BackgroundColor = color(theme.BackgroundMuted)

	chroma := *cfg.CodeBlock.Chroma
	chroma.Text.Color = color(theme.Foreground)
	chroma.Error.Color = color(theme.Danger)
	chroma.Error.BackgroundColor = color(theme.BackgroundMuted)
	chroma.Comment.Color = color(theme.SyntaxComment)
	chroma.CommentPreproc.Color = color(theme.SyntaxKeyword)
	chroma.Keyword.Color = color(theme.SyntaxKeyword)
	chroma.KeywordReserved.Color = color(theme.SyntaxKeyword)
	chroma.KeywordNamespace.Color = color(theme.SyntaxKeyword)
	chroma.KeywordType.Color = color(theme.SyntaxEntity)
	chroma.Operator.Color = color(theme.Foreground)
	chroma.Punctuation.Color = color(theme.Foreground)
	chroma.Name.Color = color(theme.Foreground)
	chroma.NameBuiltin.Color = color(theme.SyntaxEntity)
	chroma.NameTag.Color = color(theme.SyntaxTag)
	chroma.NameAttribute.Color = color(theme.SyntaxVariable)
	chroma.NameClass.Color = color(theme.SyntaxEntity)
	chroma.NameDecorator.Color = color(theme.SyntaxEntity)
	chroma.NameFunction.Color = color(theme.SyntaxEntity)
	chroma.LiteralNumber.Color = color(theme.SyntaxConstant)
	chroma.LiteralString.Color = color(theme.SyntaxString)
	chroma.LiteralStringEscape.Color = color(theme.SyntaxTag)
	chroma.GenericDeleted.Color = color(theme.Danger)
	chroma.GenericInserted.Color = color(theme.Success)
	chroma.GenericSubheading.Color = color(theme.Muted)
	chroma.Background.BackgroundColor = color(theme.BackgroundMuted)
	cfg.CodeBlock.Chroma = &chroma
	return cfg
}

func color(value string) *string { return &value }
