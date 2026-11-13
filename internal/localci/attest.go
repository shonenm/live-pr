package localci

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/shonenm/live-pr/v2/internal/filelock"
)

// VerifyScript is the remote-side verifier: it checks the attestation for the
// checked-out tree and writes `<job>=attested` lines to $GITHUB_OUTPUT.
//
//go:embed verify.sh
var VerifyScript string

const (
	notesRef = "refs/notes/live-pr"
	// originNotesRef holds origin's notes while they are merged into notesRef.
	originNotesRef = "refs/notes/live-pr-origin"
	sigNamespace   = "live-pr"
)

// Attestation is the signed statement that jobs passed against a tree. The
// tree hash covers .live-pr.toml and the scripts it calls, so the definitions
// that ran are bound without hashing them separately.
type Attestation struct {
	Version int      `json:"version"`
	Tree    string   `json:"tree"`
	Jobs    []string `json:"jobs"`
	Time    string   `json:"time"`
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

// Attest stores the result as a git note on the tree object, signed with the
// SSH key at keyPath (a private key, or a public key whose private half is
// held by ssh-agent). Without keyPath the note is unsigned and never replaces
// an existing one, which may be signed.
func Attest(root, keyPath, tree string, jobs []string) error {
	jobs = append([]string(nil), jobs...)
	sort.Strings(jobs)
	payload, err := json.Marshal(Attestation{Version: 1, Tree: tree, Jobs: jobs, Time: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	var sig []byte
	if keyPath != "" {
		if sig, err = sign(keyPath, payload); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Sign before taking the lock: the key may wait for the user to approve.
	release, err := lockNotes(ctx, root)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	syncNotes(ctx, root)
	if sig == nil {
		if _, err := git(ctx, root, nil, "notes", "--ref="+notesRef, "show", tree); err == nil {
			return errors.New("the tree already has an attestation, and an unsigned one does not replace it")
		}
	}
	_, err = git(ctx, root, append(payload, sig...), "notes", "--ref="+notesRef, "add", "-f", "-F", "-", tree)
	return err
}

func sign(keyPath string, payload []byte) ([]byte, error) {
	if strings.HasPrefix(keyPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		keyPath = filepath.Join(home, keyPath[2:])
	}
	cmd := exec.Command("ssh-keygen", "-Y", "sign", "-f", keyPath, "-n", sigNamespace)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	sig, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sign attestation: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return sig, nil
}

// PushNotes publishes the attestation notes to origin. When another machine
// pushed first, it merges origin's notes and pushes again.
func PushNotes(root string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	release, err := lockNotes(ctx, root)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	for attempt := 1; ; attempt++ {
		_, err = git(ctx, root, nil, "push", "-q", "origin", notesRef)
		if err == nil || attempt == 3 {
			return err
		}
		syncNotes(ctx, root)
	}
}

// lockNotes serializes note writes across the worktrees of one repository,
// which share the notes ref.
func lockNotes(ctx context.Context, root string) (func() error, error) {
	dir, err := git(ctx, root, nil, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filelock.Acquire(filepath.Join(dir, "live-pr-notes"))
}

// syncNotes merges origin's notes into the local ref, keeping the local note
// when both have one for the same tree. Replacing the ref with origin's would
// drop notes not pushed yet. Without origin or its notes, the local ref stays.
func syncNotes(ctx context.Context, root string) {
	if _, err := git(ctx, root, nil, "fetch", "-q", "origin", "+"+notesRef+":"+originNotesRef); err != nil {
		return
	}
	_, _ = git(ctx, root, nil, "notes", "--ref="+notesRef, "merge", "-q", "-s", "ours", originNotesRef)
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

// Verify returns the jobs that a signer listed in .live-pr/allowed_signers at
// base attested for tree, as the remote verifier (verify.sh) would. reason
// says why nothing is attested. Origin's notes are merged in first.
func Verify(ctx context.Context, root, base, tree string) (jobs []string, reason string) {
	if release, err := lockNotes(ctx, root); err == nil {
		syncNotes(ctx, root)
		_ = release()
	}
	signers, err := git(ctx, root, nil, "show", base+":.live-pr/allowed_signers")
	if err != nil {
		return nil, "no .live-pr/allowed_signers at " + base
	}
	note, err := git(ctx, root, nil, "notes", "--ref="+notesRef, "show", tree)
	if err != nil {
		return nil, "no attestation for tree " + tree
	}
	payload, sig, _ := strings.Cut(note, "\n")
	if sig == "" {
		return nil, "attestation is not signed"
	}
	dir, err := os.MkdirTemp("", "live-pr-verify-")
	if err != nil {
		return nil, err.Error()
	}
	defer func() { _ = os.RemoveAll(dir) }()
	signersFile, sigFile := filepath.Join(dir, "signers"), filepath.Join(dir, "sig")
	if err := os.WriteFile(signersFile, []byte(signers+"\n"), 0o600); err != nil {
		return nil, err.Error()
	}
	if err := os.WriteFile(sigFile, []byte(sig+"\n"), 0o600); err != nil {
		return nil, err.Error()
	}
	check := exec.CommandContext(ctx, "ssh-keygen", "-Y", "verify", "-f", signersFile, "-I", sigNamespace, "-n", sigNamespace, "-s", sigFile)
	check.Stdin = strings.NewReader(payload + "\n")
	if err := check.Run(); err != nil {
		return nil, "attestation signature is not trusted"
	}
	var att Attestation
	// A note can be copied onto another tree, so the payload must name this one.
	if json.Unmarshal([]byte(payload), &att) != nil || att.Tree != tree {
		return nil, "attestation is for another tree"
	}
	return att.Jobs, ""
}

// Execute runs jobs and, when the result is attributable to one committed
// tree, signs and publishes the attestation. When a signer trusted at base
// already attested every job for the tree, nothing runs unless force is set.
// The returned note says what was attested or why nothing was.
func Execute(ctx context.Context, root, keyPath, base string, force bool, jobs []Job, emit func(Event)) (passed []string, note string) {
	tree, clean, err := Tree(root)
	if err != nil {
		return nil, "not started: " + err.Error()
	}
	if clean && !force && base != "" {
		if attested, _ := Verify(ctx, root, base, tree); covers(attested, jobs) {
			for _, job := range jobs {
				for i := range job.Steps {
					emit(Event{Job: job.ID, Step: i, State: StateSkipped})
				}
				passed = append(passed, job.ID)
			}
			return passed, "already attested " + strings.Join(passed, ", ") + " for tree " + tree[:12] + "; nothing ran"
		}
	}
	passed = Run(ctx, root, jobs, emit)
	switch {
	case len(passed) == 0:
		return passed, "not attested: no job passed"
	case !clean:
		return passed, "not attested: the checkout had uncommitted changes"
	}
	if after, stillClean, err := Tree(root); err != nil || after != tree || !stillClean {
		return passed, "not attested: the checkout changed during the run"
	}
	if err := Attest(root, keyPath, tree, passed); err != nil {
		return passed, "not attested: " + err.Error()
	}
	note = "attested " + strings.Join(passed, ", ") + " for tree " + tree[:12]
	if keyPath == "" {
		note += " without a signature (ci.signing_key is not set)"
	}
	if err := PushNotes(root); err != nil {
		note += " (not pushed: " + err.Error() + ")"
	}
	return passed, note
}

func covers(attested []string, jobs []Job) bool {
	if len(jobs) == 0 {
		return false
	}
	for _, job := range jobs {
		if !slices.Contains(attested, job.ID) {
			return false
		}
	}
	return true
}
