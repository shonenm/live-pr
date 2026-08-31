package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shonenm/live-pr/internal/atomicfile"
)

// ViewsPath reports the config file that owns the view list: the per-repo
// file when it already defines views, otherwise the global one. Editing views
// in the TUI writes there, so a repo-scoped setup keeps its scope.
func ViewsPath(repoRoot string) string {
	repoConfig := filepath.Join(repoRoot, ".live-pr.toml")
	if data, err := os.ReadFile(repoConfig); err == nil && hasViewsSection(string(data)) {
		return repoConfig
	}
	return filepath.Join(globalConfigDir(), "live-pr", "config.toml")
}

// SaveViews rewrites only the [[views]] tables in path, leaving every other
// setting — and the comments and layout around them — exactly as written.
func SaveViews(path string, views []View) error {
	views = NormalizeViews(views)
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	updated := replaceViewsSection(string(existing), renderViews(views))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.WriteString(updated); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(name, path)
}

func renderViews(views []View) string {
	var b strings.Builder
	for _, view := range views {
		b.WriteString("[[views]]\n")
		fmt.Fprintf(&b, "name = %s\n", quote(view.Name))
		fmt.Fprintf(&b, "query = %s\n", quote(view.Query))
		b.WriteString("\n")
	}
	return b.String()
}

// quote emits a TOML basic string, escaping what the format requires.
func quote(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`, "\r", `\r`)
	return `"` + replacer.Replace(s) + `"`
}

// replaceViewsSection swaps every [[views]] table for the rendered block,
// keeping surrounding content. Without an existing block the views are
// appended, which keeps a hand-written config readable.
func replaceViewsSection(existing, rendered string) string {
	lines := strings.Split(existing, "\n")
	kept := make([]string, 0, len(lines))
	insertAt := -1
	inViews := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isTableHeader(trimmed) {
			// A [[views]] header opens the region; any other header ends it.
			inViews = trimmed == "[[views]]"
			if inViews && insertAt < 0 {
				insertAt = len(kept)
			}
		}
		if inViews {
			continue
		}
		kept = append(kept, line)
	}
	if insertAt < 0 {
		body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
		if body != "" {
			body += "\n\n"
		}
		return body + rendered
	}
	// Drop blank padding that belonged to the removed region.
	for insertAt > 0 && strings.TrimSpace(kept[insertAt-1]) == "" {
		kept = append(kept[:insertAt-1], kept[insertAt:]...)
		insertAt--
	}
	head := strings.Join(kept[:insertAt], "\n")
	if head != "" {
		head += "\n\n"
	}
	tail := strings.TrimLeft(strings.Join(kept[insertAt:], "\n"), "\n")
	return head + rendered + tail
}

func isTableHeader(trimmed string) bool {
	return strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")
}

// replaceFile renames over the target, retrying on Windows where rename onto
// an existing file fails.
func replaceFile(tmp, path string) error { return atomicfile.Replace(tmp, path) }

func hasViewsSection(data string) bool {
	for _, line := range strings.Split(data, "\n") {
		if strings.TrimSpace(line) == "[[views]]" {
			return true
		}
	}
	return false
}
