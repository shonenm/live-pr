// Package tui renders the living PR: a GitHub-PR conversation timeline (left)
// with the diff of the selected commit (right).
package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shonenm/live-pr/internal/config"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	"github.com/shonenm/live-pr/internal/review"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/shonenm/live-pr/internal/timeline"
)

type keyMap struct {
	Up, Down, Open, Help, Quit key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Open, k.Help, k.Quit}
}
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down}, {k.Open, k.Help, k.Quit}}
}

var keys = keyMap{
	Up:   key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k/↑", "up")),
	Down: key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j/↓", "down")),
	Open: key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "review commit")),
	Help: key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit: key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}

// reviewerDone is delivered after the external reviewer process exits.
type reviewerDone struct{ err error }

// Model holds the living-PR view state.
type Model struct {
	title      string
	base, head string
	events     []event.Event
	cursor     int
	reviewer   string
	status     string

	conv  viewport.Model // left: conversation timeline
	diff  viewport.Model // right: diff of the selected commit
	help  help.Model
	keys  keyMap
	w, h  int
	ready bool
}

// New builds a model from the current branch's store.
func New() (Model, error) {
	st, err := store.Discover()
	if err != nil {
		return Model{}, err
	}
	base := git.DefaultBase()
	_, _ = timeline.SyncCommits(st.Timeline(), base)

	events, err := event.Load(st.Timeline())
	if err != nil {
		return Model{}, err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })

	return Model{
		title:    deriveTitle(st.Conclusion(), st.Branch),
		base:     base,
		head:     st.Branch,
		events:   events,
		reviewer: config.Load(st.Root).Reviewer,
		help:     help.New(),
		keys:     keys,
	}, nil
}

// Run launches the TUI.
func Run() error {
	m, err := New()
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m Model) current() *event.Event {
	if m.cursor < 0 || m.cursor >= len(m.events) {
		return nil
	}
	return &m.events[m.cursor]
}

func (m Model) Init() tea.Cmd { return nil }

const (
	headerLines = 4
	footerLines = 1
	convRatio   = 52 // percent of width for the conversation pane
)

func (m *Model) layout() {
	convW := m.w * convRatio / 100
	if convW < 20 {
		convW = 20
	}
	diffW := m.w - convW - 3 // border + padding + gap
	if diffW < 10 {
		diffW = 10
	}
	bodyH := m.h - headerLines - footerLines
	if bodyH < 3 {
		bodyH = 3
	}
	if !m.ready {
		m.conv = viewport.New(convW, bodyH)
		m.diff = viewport.New(diffW, bodyH)
		m.ready = true
	} else {
		m.conv.Width, m.conv.Height = convW, bodyH
		m.diff.Width, m.diff.Height = diffW, bodyH
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		m.sync()
		return m, nil

	case reviewerDone:
		if msg.err != nil {
			m.status = "reviewer: " + msg.err.Error()
		} else {
			m.status = ""
		}
		return m, nil

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			return m, nil
		case key.Matches(msg, m.keys.Open):
			if e := m.current(); e != nil && e.Kind == event.Commit && e.SHA != "" {
				cmd := review.Command(m.reviewer, e.SHA, m.base, m.head)
				return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return reviewerDone{err} })
			}
			m.status = "select a commit to review"
			return m, nil
		case key.Matches(msg, m.keys.Down):
			if m.cursor < len(m.events)-1 {
				m.cursor++
				m.sync()
			}
			return m, nil
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
				m.sync()
			}
			return m, nil
		}
	}
	// Let unhandled messages scroll the diff pane.
	var cmd tea.Cmd
	m.diff, cmd = m.diff.Update(msg)
	return m, cmd
}

// sync rebuilds both panes for the current selection.
func (m *Model) sync() {
	if !m.ready {
		return
	}
	content, selStart := m.buildConv()
	m.conv.SetContent(content)
	if off := selStart - 1; off > 0 {
		m.conv.SetYOffset(off)
	} else {
		m.conv.GotoTop()
	}
	m.diff.SetContent(m.buildDiff())
	m.diff.GotoTop()
}

func (m Model) View() string {
	if !m.ready {
		return "loading…"
	}
	diff := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
		BorderForeground(lipgloss.Color(cBorder)).PaddingLeft(1).
		Render(m.diff.View())
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.conv.View(), diff)
	return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderFooter())
}

func (m Model) renderHeader() string {
	nCommits := 0
	for _, e := range m.events {
		if e.Kind == event.Commit {
			nCommits++
		}
	}
	openBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(cOpen)).Foreground(lipgloss.Color("#ffffff")).
		Padding(0, 1).Render("⇄ Open")
	l1 := openBadge + "  " + stBold.Render(m.title)
	l2 := stMuted.Render("⎇ ") + stBold.Render(m.base) + stMuted.Render(" ← ") + stFg.Render(m.head) +
		stMuted.Render(fmt.Sprintf("   · %d commits · %d events", nCommits, len(m.events)))
	tabs := stBold.Render("● Conversation") + stMuted.Render("   Files changed   Commits "+itoa(nCommits))
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(cBorder)).Render(strings.Repeat("─", max(0, m.w)))
	return lipgloss.JoinVertical(lipgloss.Left, l1, l2, tabs, rule)
}

// buildConv renders the full conversation feed; returns it plus the line index
// where the selected event starts (for scroll positioning).
func (m Model) buildConv() (string, int) {
	if len(m.events) == 0 {
		return stMuted.Render("(no events yet — try `live-pr note …`)"), 0
	}
	width := m.conv.Width
	var lines []string
	selStart := 0
	for i, e := range m.events {
		if i == m.cursor {
			selStart = len(lines)
		}
		lines = append(lines, m.eventLines(e, i == m.cursor, width)...)
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n"), selStart
}

// eventLines renders one event as a GitHub-PR conversation item (full content).
func (m Model) eventLines(e event.Event, selected bool, width int) []string {
	bar := "  "
	if selected {
		bar = stAccent.Render("▌") + " "
	}
	who := "🤖 claude-agent"
	if e.Kind == event.Note {
		who = "👤 you"
	}
	var hdr string
	if e.Kind == event.Commit {
		hdr = kindLabel(e.Kind) + " " + stGreenF.Render(e.SHA) + stMuted.Render(" · "+shortTS(e.TS))
	} else {
		hdr = stMuted.Render(who+" · ") + kindLabel(e.Kind) + stMuted.Render(" · "+shortTS(e.TS))
	}

	lines := []string{bar + hdr}
	for _, ln := range wrapLines(e.Title, stBold, width-3) {
		lines = append(lines, bar+ln)
	}
	if body := strings.TrimSpace(e.Body); body != "" {
		for _, para := range strings.Split(body, "\n") {
			for _, ln := range wrapLines(para, stMuted, width-3) {
				lines = append(lines, bar+ln)
			}
		}
	}
	return lines
}

// buildDiff fills the diff slot: the selected commit's diff, else a placeholder.
func (m Model) buildDiff() string {
	e := m.current()
	if e == nil {
		return stMuted.Render("no events")
	}
	if e.Kind == event.Commit && e.SHA != "" {
		if d := git.Show(e.SHA); d != "" {
			return d
		}
		return stMuted.Render("(commit " + e.SHA + " not found in this repo)")
	}
	head := stMuted.Render("— " + string(e.Kind) + " · no diff —")
	body := lipgloss.NewStyle().Width(m.diff.Width).Render(strings.TrimSpace(e.Body))
	return head + "\n\n" + body
}

func (m Model) renderFooter() string {
	if m.status != "" {
		return stRedF.Render(m.status)
	}
	return m.help.View(m.keys)
}

func deriveTitle(conclusionPath, branch string) string {
	if data, err := os.ReadFile(conclusionPath); err == nil {
		for _, ln := range strings.Split(string(data), "\n") {
			ln = strings.TrimSpace(strings.TrimLeft(ln, "# "))
			if ln != "" && ln != "<title>" && !strings.HasPrefix(ln, "<current conclusion") {
				return ln
			}
		}
	}
	return branch
}

func wrapLines(text string, style lipgloss.Style, width int) []string {
	if width < 8 {
		width = 8
	}
	return strings.Split(style.Width(width).Render(text), "\n")
}

func shortTS(ts string) string { return strings.Replace(strings.TrimPrefix(ts, "2026-"), "T", " ", 1) }

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
