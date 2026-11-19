package tui

import (
	"context"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/shonenm/live-pr/v2/internal/git"
	gh "github.com/shonenm/live-pr/v2/internal/github"
	"github.com/shonenm/live-pr/v2/internal/localci"
	"github.com/shonenm/live-pr/v2/internal/prfilter"
)

type localCIEvent struct {
	event  localci.Event
	events <-chan tea.Msg
}

type localCIDone struct{ note string }

// localCIState is the checks view's record of the last local run. Rows
// reuse woodpeckerStep so local and remote steps render identically.
type localCIState struct {
	jobs    []localci.Job
	rows    [][]woodpeckerStep
	failure string
	note    string
	running bool
}

func (m Model) canRunLocalCI() bool {
	return !m.remote && len(m.localCIConfig.Checks) > 0 && !m.localCI.running
}

// startLocalCI runs the configured checks in the checkout and streams
// step events back through a channel drained one message at a time.
func (m *Model) startLocalCI() tea.Cmd {
	jobs := localci.Jobs(m.localCIConfig.Checks)
	m.localCI = localCIState{jobs: jobs, running: true}
	for _, job := range jobs {
		rows := make([]woodpeckerStep, len(job.Steps))
		for i, step := range job.Steps {
			rows[i] = woodpeckerStep{workflow: job.ID, name: step.Name, state: "pending"}
		}
		m.localCI.rows = append(m.localCI.rows, rows)
	}
	events := make(chan tea.Msg, 16)
	root, key := m.root, m.ciSigningKey
	// Signers are trusted as the remote verifier trusts them: from the base.
	base := git.DefaultBase()
	if m.cache.PR != nil && m.cache.PR.BaseRefOID != "" {
		base = m.cache.PR.BaseRefOID
	}
	go func() {
		_, note := localci.Execute(context.Background(), root, key, base, false, jobs, func(ev localci.Event) {
			events <- localCIEvent{event: ev, events: events}
		})
		events <- localCIDone{note: note}
	}()
	return waitLocalCI(events)
}

func waitLocalCI(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-events }
}

func (m *Model) applyLocalCIEvent(ev localci.Event) {
	for i, job := range m.localCI.jobs {
		if job.ID != ev.Job || ev.Step >= len(m.localCI.rows[i]) {
			continue
		}
		row := &m.localCI.rows[i][ev.Step]
		row.state = ev.State
		if ev.Duration > 0 {
			row.duration = ev.Duration.String()
		}
		if ev.Output != "" {
			m.localCI.failure = ev.Output
		}
	}
}

// localCILines renders the local run above the GitHub checks.
func (m Model) localCILines() []string {
	if len(m.localCI.jobs) == 0 && m.localCI.note == "" {
		return nil
	}
	lines := []string{stFg.Bold(true).Render("Local CI")}
	for i, job := range m.localCI.jobs {
		lines = append(lines, "  "+stFg.Render(job.ID))
		for _, step := range m.localCI.rows[i] {
			lines = append(lines, woodpeckerStepLine("    └─ ", step))
		}
	}
	if m.localCI.failure != "" {
		lines = append(lines, "", stRedF.Render(strings.ReplaceAll(m.localCI.failure, "\t", "    ")))
	}
	if m.localCI.note != "" {
		lines = append(lines, "", stMuted.Render(m.localCI.note))
	}
	return append(lines, "")
}

// mergeAttestation is what the local verifier found for a PR's head tree.
type mergeAttestation struct {
	number int
	jobs   []string
	reason string
}

// verifyMergeAttestation checks pr's head tree the way the remote verifier
// would, so the merge confirmation can tell an attested check from one that
// GitHub merely reports as skipped.
func verifyMergeAttestation(root string, pr gh.PR) tea.Cmd {
	return func() tea.Msg {
		tree, err := git.RevisionTree(pr.HeadRefOID)
		if err != nil {
			return mergeAttestation{number: pr.Number, reason: "head " + pr.HeadRefOID + " is not fetched"}
		}
		base := pr.BaseRefOID
		if base == "" {
			base = git.DefaultBase()
		}
		jobs, reason := localci.Verify(context.Background(), root, base, tree)
		return mergeAttestation{number: pr.Number, jobs: jobs, reason: reason}
	}
}

// localCheckLines gives each configured local check its state for the merge
// confirmation: attested locally, or else what GitHub reports for the check
// of the same name.
func (o prActionOverlay) localCheckLines(m Model) []string {
	if m.remote || len(m.localCIConfig.Checks) == 0 {
		return nil
	}
	if o.attestation == nil {
		return []string{stMuted.Render("Local CI: verifying attestation…")}
	}
	lines := []string{stMuted.Render("Local CI")}
	for _, job := range localci.Jobs(m.localCIConfig.Checks) {
		lines = append(lines, "  "+localCheckState(job.ID, o.attestation.jobs, o.pr))
	}
	if len(o.attestation.jobs) == 0 && o.attestation.reason != "" {
		lines = append(lines, "  "+stMuted.Render(o.attestation.reason))
	}
	return lines
}

func localCheckState(id string, attested []string, pr gh.PR) string {
	if slices.Contains(attested, id) {
		return stGreenF.Render("✓ " + id + " attested")
	}
	if !pr.PreviewLoaded && len(pr.Checks) == 0 {
		return stMuted.Render("· " + id + " CI loading")
	}
	for _, check := range pr.Checks {
		if check.Name != id {
			continue
		}
		if strings.EqualFold(check.Conclusion, "SKIPPED") {
			return stAttention.Render("◌ " + id + " skipped remotely")
		}
		switch _, failed, passed := prfilter.CheckCounts([]gh.PRCheck{check}); {
		case failed > 0:
			return stRedF.Render("✗ " + id + " failed")
		case passed > 0:
			return stGreenF.Render("✓ " + id + " passed")
		default:
			return stAttention.Render("◌ " + id + " running remotely")
		}
	}
	return stMuted.Render("· " + id + " not reported")
}
