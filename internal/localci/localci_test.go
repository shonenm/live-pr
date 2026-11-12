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

	// A valid note copied onto a different tree must not carry over.
	sh(`echo more > file && git commit -q -am more && git notes --ref=live-pr copy "` + tree + `" "$(git rev-parse 'HEAD^{tree}')"`)
	if out := verify(); strings.Contains(out, "=attested") {
		t.Fatalf("attestation followed a copied note: %s", out)
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

	sh(`echo dirty > untracked`)
	if _, clean, _ := Tree(root); clean {
		t.Error("Tree reported a checkout with untracked files as clean")
	}
}
