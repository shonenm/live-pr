package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/publish"
	"github.com/spf13/cobra"
)

func prTestCommand() (*cobra.Command, *bytes.Buffer) {
	cmd, out := &cobra.Command{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.Flags().String("base", "", "")
	cmd.Flags().Bool("draft", false, "")
	cmd.Flags().Bool("force-managed-body", false, "")
	cmd.Flags().Bool("dry-run", false, "")
	return cmd, out
}

func restorePRCommands(t *testing.T) {
	t.Helper()
	preview, publish := buildPRPreview, publishPullRequest
	t.Cleanup(func() { buildPRPreview, publishPullRequest = preview, publish })
}

func TestRunPreviewPrintsAssembledPR(t *testing.T) {
	restorePRCommands(t)
	gotBase := ""
	buildPRPreview = func(base string) (publish.Preview, error) {
		gotBase = base
		return publish.Preview{Title: "Title", Body: "Body\n", Base: "release", Head: "feature"}, nil
	}
	cmd, out := prTestCommand()
	_ = cmd.Flags().Set("base", "release")
	if err := runPreview(cmd); err != nil {
		t.Fatal(err)
	}
	if gotBase != "release" || !strings.Contains(out.String(), "# Title\n_(base: release ← feature)_\n\nBody") {
		t.Fatalf("preview = base:%q output:%q", gotBase, out.String())
	}
}

func TestRunPublishForwardsOptionsAndReportsResult(t *testing.T) {
	restorePRCommands(t)
	var got publish.Options
	publishPullRequest = func(opts publish.Options) (publish.Result, error) {
		got = opts
		return publish.Result{PR: gh.PR{URL: "https://example/pr/12"}, Created: true}, nil
	}
	cmd, out := prTestCommand()
	_ = cmd.Flags().Set("base", "release")
	_ = cmd.Flags().Set("draft", "true")
	_ = cmd.Flags().Set("force-managed-body", "true")
	if err := runPublish(cmd); err != nil {
		t.Fatal(err)
	}
	if got.Base != "release" || !got.Draft || !got.ForceManagedBody || out.String() != "https://example/pr/12\n" {
		t.Fatalf("publish = options:%#v output:%q", got, out.String())
	}

	publishPullRequest = func(publish.Options) (publish.Result, error) {
		return publish.Result{PR: gh.PR{URL: "https://example/pr/12"}}, nil
	}
	out.Reset()
	if err := runPublish(cmd); err != nil || out.String() != "updated https://example/pr/12\n" {
		t.Fatalf("update output = %q err=%v", out.String(), err)
	}
}

func TestRunPRDryRunAndPublishFailure(t *testing.T) {
	restorePRCommands(t)
	previewCalls, publishCalls := 0, 0
	buildPRPreview = func(string) (publish.Preview, error) {
		previewCalls++
		return publish.Preview{Title: "Dry", Base: "main", Head: "feature"}, nil
	}
	boom := errors.New("publish failed")
	publishPullRequest = func(publish.Options) (publish.Result, error) {
		publishCalls++
		return publish.Result{}, boom
	}
	cmd, out := prTestCommand()
	_ = cmd.Flags().Set("dry-run", "true")
	if err := runPR(cmd, nil); err != nil || previewCalls != 1 || publishCalls != 0 || !strings.Contains(out.String(), "# Dry") {
		t.Fatalf("dry run = preview:%d publish:%d output:%q err=%v", previewCalls, publishCalls, out.String(), err)
	}
	_ = cmd.Flags().Set("dry-run", "false")
	out.Reset()
	if err := runPR(cmd, nil); !errors.Is(err, boom) || publishCalls != 1 || out.Len() != 0 {
		t.Fatalf("failure = publish:%d output:%q err=%v", publishCalls, out.String(), err)
	}
}
