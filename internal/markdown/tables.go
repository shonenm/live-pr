package markdown

import "strings"

type mdPart struct {
	table bool
	text  string
}

// splitMarkdownParts isolates GFM pipe tables from the rest of the comment.
// Tables need glamour's pane-width cell wrap; everything else still uses the
// wide renderer plus kinsoku wrapping so Japanese prose does not break early
// at ASCII word boundaries.
func splitMarkdownParts(src string) []mdPart {
	lines := strings.Split(src, "\n")
	n := len(lines)
	inTable := make([]bool, n)
	inFence := false
	fence := ""
	for i := 0; i < n; i++ {
		if mark := fenceMarker(lines[i]); mark != "" {
			if !inFence {
				inFence, fence = true, mark
			} else if mark == fence {
				inFence, fence = false, ""
			}
			continue
		}
		if inFence || i+1 >= n || !hasPipe(lines[i]) || !isTableDelimiter(lines[i+1]) {
			continue
		}
		end := i + 2
		for end < n && hasPipe(lines[end]) && strings.TrimSpace(lines[end]) != "" {
			end++
		}
		for j := i; j < end; j++ {
			inTable[j] = true
		}
		i = end - 1
	}

	var parts []mdPart
	var b strings.Builder
	table := false
	flush := func() {
		if b.Len() == 0 {
			return
		}
		parts = append(parts, mdPart{table: table, text: b.String()})
		b.Reset()
	}
	for i, line := range lines {
		if inTable[i] != table && b.Len() > 0 {
			flush()
		}
		table = inTable[i]
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	flush()
	if len(parts) == 0 {
		return []mdPart{{text: src}}
	}
	return parts
}

func hasPipe(line string) bool {
	return strings.Contains(line, "|")
}

func fenceMarker(line string) string {
	s := strings.TrimLeft(line, " \t")
	switch {
	case strings.HasPrefix(s, "```"):
		return "```"
	case strings.HasPrefix(s, "~~~"):
		return "~~~"
	default:
		return ""
	}
}

func isTableDelimiter(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "|") || !strings.Contains(trimmed, "-") {
		return false
	}
	for _, cell := range strings.Split(strings.Trim(trimmed, "|"), "|") {
		cell = strings.TrimSpace(cell)
		if cell == "" || !isDelimiterCell(cell) {
			return false
		}
	}
	return true
}

func isDelimiterCell(cell string) bool {
	i := 0
	if i < len(cell) && cell[i] == ':' {
		i++
	}
	dashes := 0
	for i < len(cell) && cell[i] == '-' {
		dashes++
		i++
	}
	if dashes == 0 {
		return false
	}
	if i < len(cell) && cell[i] == ':' {
		i++
	}
	return i == len(cell)
}
