// Package review builds the external reviewer command from a user template.
package review

import (
	"os/exec"
	"strings"
)

// Command expands a reviewer template ({sha}/{base}/{head}) and returns a
// command to run it through the shell, so templates may contain quotes/pipes
// (e.g. `git show {sha} | delta | less -R`).
func Command(tmpl, sha, base, head string) *exec.Cmd {
	line := strings.NewReplacer(
		"{sha}", sha,
		"{base}", base,
		"{head}", head,
	).Replace(tmpl)
	return exec.Command("sh", "-c", line)
}

// expand is the substitution alone, exposed for tests.
func expand(tmpl, sha, base, head string) string {
	return strings.NewReplacer("{sha}", sha, "{base}", base, "{head}", head).Replace(tmpl)
}
