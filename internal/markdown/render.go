// Package markdown renders GitHub-flavored comment text for the terminal.
package markdown

import (
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

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

// glamourWrapWidth is deliberately far wider than any pane. Glamour only
// wraps at ASCII word boundaries, so for space-free runs like Japanese it
// either leaves lines overflowing or — in mixed English/Japanese text —
// breaks early next to an English word and wastes the rest of the line.
// Render lets glamour style only, then wraps itself with ansi.WrapWc, which
// prefers word boundaries but breaks anywhere when a run of wide characters
// fills the line.
// ponytail: tables wider than this degrade; re-enable glamour wrapping with
// per-element handling if GitHub-comment tables ever matter.
const glamourWrapWidth = 500

var renderer = struct {
	sync.Mutex
	r *glamour.TermRenderer
}{}

func rendererFor() (*glamour.TermRenderer, error) {
	renderer.Lock()
	defer renderer.Unlock()
	if renderer.r != nil {
		return renderer.r, nil
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(githubStyle()),
		glamour.WithWordWrap(glamourWrapWidth),
	)
	if err != nil {
		return nil, err
	}
	renderer.r = r
	return r, nil
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

	wr, err := rendererFor()
	if err != nil {
		return text
	}
	renderer.Lock()
	out, err := wr.Render(text)
	renderer.Unlock()
	if err != nil {
		return text
	}
	out = wrapRendered(out, width)

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

// csiSeq matches one ANSI CSI escape sequence (glamour output is SGR-only in
// practice, but any CSI terminator is accepted so nothing is half-consumed).
var csiSeq = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// trimTrailingSpace removes the space padding glamour appends to block lines
// (list items are padded to the full wrap width). Escape sequences interspersed
// with the padding are dropped along with it; a reset is appended when the cut
// could leave a style open.
func trimTrailingSpace(s string) string {
	last := 0 // end of the last printable non-space rune
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			if m := csiSeq.FindStringIndex(s[i:]); m != nil && m[0] == 0 {
				i += m[1]
			} else {
				i++
			}
			continue
		}
		if s[i] != ' ' {
			last = i + 1
		}
		i++
	}
	if last == len(s) || last == 0 {
		return s[:last]
	}
	if strings.Contains(s[:last], "\x1b") && !strings.HasSuffix(s[:last], "\x1b[0m") {
		return s[:last] + "\x1b[0m"
	}
	return s[:last]
}

// wrapRendered trims glamour's block padding and wraps any line wider than
// width, keeping ANSI styles intact.
func wrapRendered(s string, width int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		l = trimTrailingSpace(l)
		if xansi.StringWidth(l) > width {
			l = wrapLine(l, width)
		}
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}

// Characters that must not start a line (closing punctuation) or end one
// (opening punctuation) — basic kinsoku shori for Japanese text.
const (
	noBreakBefore = "、。，．：；！？」』）〕｝〉》」】・…\u30fc?!%)]}.,:;"
	noBreakAfter  = "「『（〔｛〈《「【([{\u201c'\""
)

func isLatinWordByte(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
		r == '\'' || r == '-' || r == '_' || r == '/' || r == '.' || r == '@' || r == ':'
}

type wrapToken struct {
	start int
	end   int // byte offset just past the token in the source string
	width int
	r     rune
}

// wrapLine greedily fills lines up to limit cells. Breaks are allowed between
// any two characters (the expected behavior for space-free runs like Japanese)
// except inside Latin words and at kinsoku-prohibited boundaries. ANSI escape
// sequences pass through with zero width; styles stay open across the inserted
// newlines, which is fine because the result is written as one block.
func wrapLine(s string, limit int) string {
	var toks []wrapToken
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			if m := csiSeq.FindStringIndex(s[i:]); m != nil && m[0] == 0 {
				toks = append(toks, wrapToken{start: i, end: i + m[1]})
				i += m[1]
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		toks = append(toks, wrapToken{start: i, end: i + size, width: runewidth.RuneWidth(r), r: r})
		i += size
	}

	canBreakAfter := func(i int) bool {
		if i < 0 || i+1 >= len(toks) || toks[i].width == 0 {
			return false
		}
		prev, next := toks[i].r, rune(0)
		for j := i + 1; j < len(toks); j++ {
			if toks[j].width > 0 {
				next = toks[j].r
				break
			}
		}
		if next == 0 {
			return false
		}
		if isLatinWordByte(prev) && isLatinWordByte(next) {
			return false // keep Latin words intact
		}
		return !strings.ContainsRune(noBreakBefore, next) && !strings.ContainsRune(noBreakAfter, prev)
	}

	var b strings.Builder
	lineStart, cur := 0, 0
	brk, brkWidth := -1, 0 // last token index a break may follow, and width up to it
	for idx, tk := range toks {
		if tk.width == 0 {
			continue
		}
		if cur+tk.width > limit && brk >= lineStart {
			b.WriteString(s[toks[lineStart].start:toks[brk].end])
			b.WriteByte('\n')
			lineStart = brk + 1
			for lineStart < len(toks) && toks[lineStart].width > 0 && toks[lineStart].r == ' ' {
				lineStart++ // drop spaces carried to the next line
			}
			cur -= brkWidth
			brk = -1
			// recompute width consumed since the new line start
		}
		cur += tk.width
		if canBreakAfter(idx) {
			brk, brkWidth = idx, cur
		}
	}
	b.WriteString(s[toks[lineStart].start:])
	return b.String()
}

// WrapText wraps pre-rendered text (which may contain ANSI styles) to width
// using the same kinsoku-aware breaking as Render. Apply it to single-line
// content such as event titles that bypass glamour.
func WrapText(s string, width int) string {
	if width < 1 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if xansi.StringWidth(l) > width {
			lines[i] = wrapLine(l, width)
		}
	}
	return strings.Join(lines, "\n")
}

func githubStyle() glamouransi.StyleConfig {
	cfg := styles.DarkStyleConfig
	// The conversation cards already pad their boxes, and glamour never wraps
	// space-free runs like Japanese (the outer lipgloss box hard-wraps those),
	// so the document margin would only narrow every wrapped line by two more
	// cells and vanish on continuation lines anyway.
	noMargin := uint(0)
	cfg.Document.Margin = &noMargin
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
