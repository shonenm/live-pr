// Package prtemplate loads GitHub-compatible default pull-request templates.
package prtemplate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/shonenm/live-pr/internal/store"
)

const defaultSummary = `# <title>

<final pull request summary — replace this after implementation; do not use an implementation plan>
`

// Load returns the repository's single default pull-request template. GitHub's
// multiple-template directories require an explicit selection and are ignored.
func Load(root string) (string, error) {
	for _, name := range []string{
		filepath.Join(".github", "pull_request_template.md"),
		"pull_request_template.md",
		filepath.Join("docs", "pull_request_template.md"),
	} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err == nil {
			return string(data), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", nil
}

// Seed creates a branch's final summary without overwriting existing work.
func Seed(st *store.Store) error {
	if _, err := os.Stat(st.Conclusion()); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	body, err := Load(st.Root)
	if err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		body = defaultSummary
	} else {
		body = "# <title>\n\n" + strings.TrimSpace(body) + "\n"
	}
	return st.WriteConclusion(body)
}
