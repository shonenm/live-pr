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

const testWorkflow = `name: CI
on: [pull_request]
env:
  TOP: top
jobs:
  test:
    needs: attest
    if: needs.attest.outputs.test != 'attested'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
      - run: echo "$TOP-$STEP" > out.txt
        env:
          STEP: step
      - name: pipefail
        shell: bash
        run: false | true
      - run: echo unreachable > skipped.txt
  expr:
    runs-on: ubuntu-latest
    steps:
      - run: echo ${{ github.sha }}
  matrix:
    strategy:
      matrix:
        os: [a, b]
    runs-on: ${{ matrix.os }}
    steps:
      - run: "true"
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: golangci/golangci-lint-action@v9
`

func writeWorkflow(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".github", "workflows", "ci.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(testWorkflow), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAndRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs steps through bash")
	}
	root := t.TempDir()
	writeWorkflow(t, root)
	jobs, err := Load(root, ".github/workflows/ci.yml", []string{"test", "expr", "matrix", "lint"}, map[string]string{"actions/setup-go": "true"})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int]string{1: "expression", 2: "matrix", 3: "replacement"} {
		if !strings.Contains(jobs[id].Unsupported, want) {
			t.Errorf("job %s: Unsupported = %q, want mention of %q", jobs[id].ID, jobs[id].Unsupported, want)
		}
	}
	if jobs[0].Unsupported != "" || len(jobs[0].Steps) != 4 {
		t.Fatalf("test job = %+v, want 4 local steps", jobs[0])
	}

	var states []string
	passed := Run(context.Background(), root, jobs, func(ev Event) {
		if ev.State != StateRunning {
			states = append(states, ev.State)
		}
	})
	if len(passed) != 0 {
		t.Errorf("passed = %v, want none (pipefail step fails)", passed)
	}
	if want := []string{StateSuccess, StateSuccess, StateFailure, StateSkipped}; !reflect.DeepEqual(states, want) {
		t.Errorf("states = %v, want %v", states, want)
	}
	if out, _ := os.ReadFile(filepath.Join(root, "out.txt")); string(out) != "top-step\n" {
		t.Errorf("out.txt = %q, want workflow and step env applied", out)
	}

	if _, err := Load(root, ".github/workflows/ci.yml", []string{"missing"}, nil); err == nil {
		t.Error("Load accepted an undefined job")
	}
}

func TestAttestVerify(t *testing.T) {
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

	if err := Attest(root, filepath.Join(keys, "trusted"), "ci.yml", tree, []string{"test", "lint"}); err != nil {
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
	if err := Attest(root, filepath.Join(keys, "rogue"), "ci.yml", tree, []string{"test"}); err != nil {
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
