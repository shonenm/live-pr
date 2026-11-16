package localci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestJobsAndRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs checks through sh")
	}
	root := t.TempDir()
	jobs := Jobs(map[string]string{
		"test": `echo "$CI" > out.txt`,
		"lint": "echo boom >&2; exit 3",
	})
	if len(jobs) != 2 || jobs[0].ID != "lint" || jobs[1].ID != "test" {
		t.Fatalf("Jobs = %+v, want lint then test", jobs)
	}

	var failure string
	passed := Run(context.Background(), root, jobs, func(ev Event) {
		if ev.State == StateFailure {
			failure = ev.Job + ": " + ev.Output
		}
	})
	if !reflect.DeepEqual(passed, []string{"test"}) {
		t.Errorf("passed = %v, want only test", passed)
	}
	if failure != "lint: boom" {
		t.Errorf("failure = %q, want the failed check's output", failure)
	}
	if out, _ := os.ReadFile(filepath.Join(root, "out.txt")); string(out) != "true\n" {
		t.Errorf("out.txt = %q, want the command run in root with CI=true", out)
	}
}

func TestAttestVerify(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the verify script targets a POSIX runner")
	}
	for _, tool := range []string{"ssh-keygen", "jq", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	root := t.TempDir()
	keys := t.TempDir()
	for key, value := range map[string]string{
		"KEYS": keys, "GITHUB_OUTPUT": "", "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_SYSTEM": os.DevNull,
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
	} {
		t.Setenv(key, value)
	}
	sh := func(script string) string {
		t.Helper()
		cmd := exec.Command("sh", "-ec", script)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
		return string(out)
	}
	script := filepath.Join(keys, "verify.sh")
	if err := os.WriteFile(script, []byte(VerifyScript), 0o755); err != nil {
		t.Fatal(err)
	}
	verify := func() string { return sh(`sh "$KEYS/verify.sh" main`) }

	sh(`ssh-keygen -q -t ed25519 -N "" -f "$KEYS/trusted" && ssh-keygen -q -t ed25519 -N "" -f "$KEYS/rogue"`)
	sh(`git init -q -b main && mkdir .live-pr && echo "live-pr $(cat "$KEYS/trusted.pub")" > .live-pr/allowed_signers && git add . && git commit -q -m base`)
	sh(`git checkout -q -b feature && echo change > file && git add . && git commit -q -m change`)

	tree, clean, err := Tree(root)
	if err != nil || !clean {
		t.Fatalf("Tree = %q, clean %v, err %v", tree, clean, err)
	}
	if out := verify(); strings.Contains(out, "=attested") {
		t.Fatalf("attested without a note: %s", out)
	}

	if err := Attest(root, filepath.Join(keys, "trusted"), tree, []string{"test", "lint"}); err != nil {
		t.Fatal(err)
	}
	if out := verify(); !strings.Contains(out, "test=attested") || !strings.Contains(out, "lint=attested") {
		t.Fatalf("trusted attestation rejected: %s", out)
	}
	if jobs, reason := Verify(context.Background(), root, "main", tree); !reflect.DeepEqual(jobs, []string{"lint", "test"}) {
		t.Fatalf("Verify = %v (%s), want what verify.sh attests", jobs, reason)
	}

	// An attested tree is not run again; the commands here would fail.
	failing := Jobs(map[string]string{"test": "exit 1", "lint": "exit 1"})
	if passed, note := Execute(context.Background(), root, "", "main", false, failing, func(Event) {}); !reflect.DeepEqual(passed, []string{"lint", "test"}) || !strings.Contains(note, "already attested") {
		t.Fatalf("Execute on an attested tree = %v, %q; want a skip", passed, note)
	}
	if passed, _ := Execute(context.Background(), root, "", "main", true, failing, func(Event) {}); len(passed) != 0 {
		t.Fatalf("forced Execute = %v, want the failing commands to run", passed)
	}

	// A pull request checks out a test merge into its base: the attestation
	// carries over only while the branch is up to date with base.
	if out := sh(`git checkout -q --detach main && git merge -q --no-ff --no-edit feature && sh "$KEYS/verify.sh" main`); !strings.Contains(out, "test=attested") {
		t.Fatalf("up-to-date merge rejected: %s", out)
	}
	if out := sh(`git checkout -q main && echo other > other && git add other && git commit -q -m other && git checkout -q --detach && git merge -q --no-ff --no-edit feature && sh "$KEYS/verify.sh" main`); strings.Contains(out, "=attested") || !strings.Contains(out, "behind base") {
		t.Fatalf("merge of a branch behind base: %s", out)
	}
	sh(`git checkout -q feature`)

	// A valid note copied onto a different tree must not carry over.
	sh(`echo more > file && git commit -q -am more && git notes --ref=live-pr copy "` + tree + `" "$(git rev-parse 'HEAD^{tree}')"`)
	if out := verify(); strings.Contains(out, "=attested") {
		t.Fatalf("attestation followed a copied note: %s", out)
	}
	copied, _, _ := Tree(root)
	if jobs, reason := Verify(context.Background(), root, "main", copied); jobs != nil || !strings.Contains(reason, "another tree") {
		t.Fatalf("Verify on a copied note = %v (%s)", jobs, reason)
	}

	// A signer added by the change under test is not trusted: keys are read
	// from the base revision.
	sh(`echo "live-pr $(cat "$KEYS/rogue.pub")" >> .live-pr/allowed_signers && git commit -q -am rogue`)
	tree, _, _ = Tree(root)
	if err := Attest(root, filepath.Join(keys, "rogue"), tree, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	if out := verify(); strings.Contains(out, "=attested") {
		t.Fatalf("self-added signer was trusted: %s", out)
	}
	if jobs, reason := Verify(context.Background(), root, "main", tree); jobs != nil || !strings.Contains(reason, "not trusted") {
		t.Fatalf("Verify trusted a self-added signer: %v (%s)", jobs, reason)
	}

	sh(`echo dirty > untracked`)
	if _, clean, _ := Tree(root); clean {
		t.Error("Tree reported a checkout with untracked files as clean")
	}
}

// The repository's own workflow runs a committed copy of the verifier.
func TestCommittedVerifyScriptMatchesEmbedded(t *testing.T) {
	committed, err := os.ReadFile(filepath.Join("..", "..", ".live-pr", "verify.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) != VerifyScript {
		t.Error(".live-pr/verify.sh is stale; regenerate it with `live-pr ci verify-script`")
	}
}

// Two clones that attest before either pushes must both end up on origin:
// the second push is rejected, merges origin's notes, and pushes again.
func TestPushNotesMergesConcurrentAttestations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signing runs ssh-keygen through a POSIX shell setup")
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	base := t.TempDir()
	for key, value := range map[string]string{
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_SYSTEM": os.DevNull,
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
	} {
		t.Setenv(key, value)
	}
	sh := func(dir, script string) string {
		t.Helper()
		cmd := exec.Command("sh", "-ec", script)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	sh(base, `ssh-keygen -q -t ed25519 -N "" -f key && git init -q --bare -b main origin.git && git clone -q origin.git a 2>/dev/null && cd a && echo a > a && git add a && git commit -q -m a && git push -q origin main && cd .. && git clone -q origin.git b`)
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	sh(b, `echo b > b && git add b && git commit -q -m b`)
	key := filepath.Join(base, "key")

	treeA, _, _ := Tree(a)
	treeB, _, _ := Tree(b)
	if err := Attest(b, key, treeB, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	if err := Attest(a, key, treeA, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	if err := PushNotes(a); err != nil {
		t.Fatal(err)
	}
	if err := PushNotes(b); err != nil {
		t.Fatalf("second push after a concurrent one: %v", err)
	}
	notes := sh(base, `git --git-dir=origin.git notes --ref=live-pr list`)
	if !strings.Contains(notes, treeA) || !strings.Contains(notes, treeB) {
		t.Fatalf("origin notes = %q, want both %s and %s", notes, treeA, treeB)
	}
}
