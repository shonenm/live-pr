// Package markdown renders GitHub-flavored comment text for the terminal.
package markdown

import (
	"fmt"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"

	"github.com/shonenm/live-pr/internal/theme"
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
		glamour.WithStyles(githubStyle()),
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
