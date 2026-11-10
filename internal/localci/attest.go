package localci

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// VerifyScript is the remote-side verifier: it checks the attestation for the
// checked-out tree and writes `<job>=attested` lines to $GITHUB_OUTPUT.
//
//go:embed verify.sh
var VerifyScript string

const (
	notesRef     = "refs/notes/live-pr"
	sigNamespace = "live-pr"
)

// Attestation is the signed statement that jobs passed against a tree. The
// tree hash covers the workflow and .live-pr.toml, so the definitions that
// ran are bound without hashing them separately.
type Attestation struct {
	Version  int      `json:"version"`
	Tree     string   `json:"tree"`
	Workflow string   `json:"workflow"`
	Jobs     []string `json:"jobs"`
	Time     string   `json:"time"`
}

// Tree returns HEAD's tree and whether the checkout matches it exactly
// (nothing staged, modified, or untracked). Ignored files are not compared:
// reusing local caches and toolchains is the point of running in place.
func Tree(root string) (tree string, clean bool, err error) {
	tree, err = git(context.Background(), root, nil, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", false, err
	}
	status, err := git(context.Background(), root, nil, "status", "--porcelain")
	return tree, status == "", err
}

// Attest signs the result with the SSH key at keyPath (a private key, or a
// public key whose private half is held by ssh-agent) and stores it as a git
// note on the tree object.
func Attest(root, keyPath, workflow, tree string, jobs []string) error {
	jobs = append([]string(nil), jobs...)
	sort.Strings(jobs)
	payload, err := json.Marshal(Attestation{Version: 1, Tree: tree, Workflow: workflow, Jobs: jobs, Time: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if strings.HasPrefix(keyPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		keyPath = filepath.Join(home, keyPath[2:])
	}
	sign := exec.Command("ssh-keygen", "-Y", "sign", "-f", keyPath, "-n", sigNamespace)
	sign.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	sign.Stderr = &stderr
	sig, err := sign.Output()
	if err != nil {
		return fmt.Errorf("sign attestation: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// ponytail: adopt the remote notes before writing so the push below is a
	// fast-forward; unpushed notes from an offline run are dropped. Use
	// `git notes merge` if concurrent offline signers ever matter.
	_, _ = git(ctx, root, nil, "fetch", "-q", "origin", "+"+notesRef+":"+notesRef)
	_, err = git(ctx, root, append(payload, sig...), "notes", "--ref="+notesRef, "add", "-f", "-F", "-", tree)
	return err
}

// PushNotes publishes the attestation notes to origin.
func PushNotes(root string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := git(ctx, root, nil, "push", "-q", "origin", notesRef)
	return err
}

func git(ctx context.Context, root string, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// Execute runs jobs and, when the result is attributable to one committed
// tree, signs and publishes the attestation. The returned note says what was
// attested or why nothing was.
func Execute(ctx context.Context, root, workflow, keyPath string, jobs []Job, emit func(Event)) (passed []string, note string) {
	tree, clean, err := Tree(root)
	if err != nil {
		return nil, "not started: " + err.Error()
	}
	passed = Run(ctx, root, jobs, emit)
	switch {
	case len(passed) == 0:
		return passed, "not attested: no job passed"
	case keyPath == "":
		return passed, "not attested: ci.signing_key is not set in the global config"
	case !clean:
		return passed, "not attested: the checkout had uncommitted changes"
	}
	if after, stillClean, err := Tree(root); err != nil || after != tree || !stillClean {
		return passed, "not attested: the checkout changed during the run"
	}
	if err := Attest(root, keyPath, workflow, tree, passed); err != nil {
		return passed, "not attested: " + err.Error()
	}
	note = "attested " + strings.Join(passed, ", ") + " for tree " + tree[:12]
	if err := PushNotes(root); err != nil {
		note += " (not pushed: " + err.Error() + ")"
	}
	return passed, note
}
