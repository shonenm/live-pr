package tui

import (
	"strings"
	"testing"

	gh "github.com/shonenm/live-pr/v2/internal/github"
)

// Sequences the Bubble Tea cell renderer would replay or honour: DCS, APC and
// an OSC 8 hyperlink.
const hostileEscapes = "\x1bP@kitty-cmd\x1b\\\x1b_Ga=T;AAAA\x1b\\\x1b]8;;https://evil.example/\x1b\\"

func assertNoHostileEscapes(t *testing.T, out string) {
	t.Helper()
	for _, bad := range []string{"\x1bP", "\x1b_", "\x1b]", "evil.example", "kitty-cmd"} {
		if strings.Contains(out, bad) {
			t.Fatalf("output leaked %q: %q", bad, out)
		}
	}
}

func TestBuildChecksSanitizesCheckAndWorkflowNames(t *testing.T) {
	m := testModel()
	m.cache.PR = &gh.PR{Checks: []gh.PRCheck{
		{Name: "lint" + hostileEscapes, WorkflowName: "CI" + hostileEscapes, Conclusion: "SUCCESS" + hostileEscapes},
		{Context: "status" + hostileEscapes, State: "PENDING"},
	}}
	m.ciCommandSteps = []woodpeckerStep{{workflow: "gate" + hostileEscapes, name: "build" + hostileEscapes, state: "success" + hostileEscapes}}
	out, _ := m.buildChecks()
	assertNoHostileEscapes(t, out)
	for _, want := range []string{"CI", "lint", "success", "status", "gate", "build"} {
		if !strings.Contains(out, want) {
			t.Fatalf("checks lost %q: %q", want, out)
		}
	}
}

func TestFormatWoodpeckerCISanitizesNames(t *testing.T) {
	out := formatWoodpeckerCI("42", "success", "ci"+hostileEscapes+"\tbuild"+hostileEscapes+"\tsuccess\t0\t0")
	assertNoHostileEscapes(t, out)
	if !strings.Contains(out, "ci") || !strings.Contains(out, "build") {
		t.Fatalf("woodpecker output lost its names: %q", out)
	}
}

func TestDeleteConfirmOverlaysSanitizeTitle(t *testing.T) {
	m := testModel()
	title := "preview" + hostileEscapes
	for name, o := range map[string]overlay{
		"local":  localDeleteOverlay{title: title},
		"remote": remoteDeleteOverlay{title: title},
		"outbox": outboxDiscardOverlay{title: title},
	} {
		t.Run(name, func(t *testing.T) {
			out := o.render(m)
			assertNoHostileEscapes(t, out)
			if !strings.Contains(out, "preview") {
				t.Fatalf("overlay lost its preview: %q", out)
			}
		})
	}
}
