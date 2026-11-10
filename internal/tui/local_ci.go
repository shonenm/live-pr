package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/shonenm/live-pr/internal/localci"
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
	return !m.remote && len(m.localCIConfig.Jobs) > 0 && !m.localCI.running
}

// startLocalCI runs the configured workflow jobs in the checkout and streams
// step events back through a channel drained one message at a time.
func (m *Model) startLocalCI() tea.Cmd {
	cfg := m.localCIConfig
	jobs, err := localci.Load(m.root, cfg.Workflow, cfg.Jobs, cfg.Uses)
	if err != nil {
		m.localCI = localCIState{note: err.Error()}
		return nil
	}
	m.localCI = localCIState{jobs: jobs, running: true}
	for _, job := range jobs {
		rows := make([]woodpeckerStep, len(job.Steps))
		for i, step := range job.Steps {
			rows[i] = woodpeckerStep{workflow: job.Name, name: step.Name, state: "pending"}
		}
		m.localCI.rows = append(m.localCI.rows, rows)
	}
	events := make(chan tea.Msg, 16)
	root, key := m.root, m.ciSigningKey
	go func() {
		_, note := localci.Execute(context.Background(), root, cfg.Workflow, key, jobs, func(ev localci.Event) {
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
		if job.Unsupported != "" {
			lines = append(lines, iconLine("  ", stMuted, "–", job.Name, "remote only", job.Unsupported))
			continue
		}
		lines = append(lines, "  "+stFg.Render(job.Name))
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
