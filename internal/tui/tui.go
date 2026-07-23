// Package tui renders the living PR as a GitHub-PR-style terminal interface.
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
	Up   key.Binding
	Down key.Binding
	Open key.Binding
	Help key.Binding
	Quit key.Binding
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
	reviewer   string // reviewer command template
	status     string // transient status line (e.g. reviewer error)

	vp    viewport.Model
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
	// Best-effort: fold any new base..HEAD commits into the timeline before load.
	_, _ = timeline.SyncCommits(st.Timeline(), base)

	events, err := event.Load(st.Timeline())
	if err != nil {
		return Model{}, err
	}
	// Display chronologically: synced commits interleave with manual events by TS.
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

// current returns the selected event, or nil when the timeline is empty.
func (m Model) current() *event.Event {
	if len(m.events) == 0 || m.cursor < 0 || m.cursor >= len(m.events) {
		return nil
	}
	return &m.events[m.cursor]
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

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		previewW, bodyH := m.paneDims()
		if !m.ready {
			m.vp = viewport.New(previewW, bodyH)
			m.ready = true
		} else {
			m.vp.Width, m.vp.Height = previewW, bodyH
		}
		m.syncPreview()
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
				m.syncPreview()
			}
			return m, nil
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
				m.syncPreview()
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	if !m.ready {
		return "loading…"
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.renderList(), m.renderPreview())
	return lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), body, m.renderFooter())
}

// --- layout helpers ----------------------------------------------------------

const (
	headerLines = 4
	footerLines = 1
	listRatio   = 44 // percent of width for the timeline list
)

func (m Model) paneDims() (previewW, bodyH int) {
	listW := m.w * listRatio / 100
	previewW = m.w - listW - 3 // border + gap
	if previewW < 10 {
		previewW = 10
	}
	bodyH = m.h - headerLines - footerLines
	if bodyH < 3 {
		bodyH = 3
	}
	return previewW, bodyH
}

func (m Model) listWidth() int { return m.w * listRatio / 100 }

// --- header ------------------------------------------------------------------

func (m Model) renderHeader() string {
	nCommits := 0
	for _, e := range m.events {
		if e.Kind == event.Commit {
			nCommits++
		}
	}
	openBadge := lipgloss.NewStyle().
		Background(lipgloss.Color(cGreen)).Foreground(lipgloss.Color("#ffffff")).
		Padding(0, 1).Render("⇄ Open")

	l1 := openBadge + "  " + stBold.Render(m.title)
	l2 := stMuted.Render("⎇ ") + stBold.Render(m.base) + stMuted.Render(" ← ") +
		stFg.Render(m.head) + stMuted.Render(fmt.Sprintf("   · %d commits · %d events", nCommits, len(m.events)))
	tabs := stBold.Render("● Conversation") + stMuted.Render("   Commits "+itoa(nCommits)+"   Files changed   Checks")
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(cBorder)).Render(strings.Repeat("─", max(0, m.w)))
	return lipgloss.JoinVertical(lipgloss.Left, l1, l2, tabs, rule)
}

// --- timeline list -----------------------------------------------------------

func (m Model) renderList() string {
	w := m.listWidth()
	_, bodyH := m.paneDims()
	var rows []string
	for i, e := range m.events {
		label, bg := kindMeta(e.Kind)
		row := pill(fmt.Sprintf("%-8s", label), bg) + " " +
			stMuted.Render(shortTS(e.TS)) + "  " + stFg.Render(e.Title)
		if e.SHA != "" {
			row += " " + stGreenF.Render(e.SHA)
		}
		// Selection: a colored bar + bold marker, no full-row background (which
		// would fight the inner pill/text ANSI resets).
		pointer := "  "
		if i == m.cursor {
			pointer = stGreenF.Render("▌ ")
		}
		rows = append(rows, truncate(pointer+row, w))
	}
	if len(rows) == 0 {
		rows = append(rows, stMuted.Render("  (no events yet — try `live-pr note …`)"))
	}
	// Window rows around the cursor so it stays visible when the timeline is tall.
	if len(rows) > bodyH {
		start := 0
		if m.cursor >= bodyH {
			start = m.cursor - bodyH + 1
		}
		end := start + bodyH
		if end > len(rows) {
			end = len(rows)
		}
		rows = rows[start:end]
	}
	content := strings.Join(rows, "\n")
	return lipgloss.NewStyle().Width(w).Height(bodyH).MaxHeight(bodyH).Render(content)
}

// --- preview -----------------------------------------------------------------

func (m *Model) syncPreview() {
	if !m.ready || len(m.events) == 0 {
		if m.ready {
			m.vp.SetContent(stMuted.Render("no events"))
		}
		return
	}
	m.vp.SetContent(m.buildPreview(m.events[m.cursor]))
	m.vp.GotoTop()
}

func (m Model) buildPreview(e event.Event) string {
	label, bg := kindMeta(e.Kind)
	who, ava := "claude-agent", "🤖"
	if e.Kind == event.Note {
		who, ava = "you", "👤"
	}
	bar := stCardBar.Width(m.vp.Width).Render(
		fmt.Sprintf(" %s %s  %s · %s", ava, who, stMuted.Render("commented"), shortTS(e.TS)))
	head := pill(label, bg) + "  " + stBold.Render(e.Title)
	body := lipgloss.NewStyle().Width(m.vp.Width).Render(e.Body)

	parts := []string{bar, "", head, "", body}
	if e.Kind == event.Commit && e.SHA != "" {
		rule := lipgloss.NewStyle().Foreground(lipgloss.Color(cBorder)).Render(strings.Repeat("─", m.vp.Width))
		stat := git.ShowStat(e.SHA)
		if stat == "" {
			stat = stMuted.Render("(commit " + e.SHA + " not found in this repo)")
		}
		parts = append(parts, "", rule, pill("◉ "+e.SHA, cGreen), "", stat,
			"", stMuted.Render("↵ open this commit in the reviewer"))
	}
	return strings.Join(parts, "\n")
}

func (m Model) renderPreview() string {
	border := lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
		BorderForeground(lipgloss.Color(cBorder)).
		PaddingLeft(1)
	return border.Render(m.vp.View())
}

// --- footer ------------------------------------------------------------------

func (m Model) renderFooter() string {
	if m.status != "" {
		return stRedF.Render(m.status)
	}
	return m.help.View(m.keys)
}

// --- small utils -------------------------------------------------------------

func deriveTitle(conclusionPath, branch string) string {
	data, err := os.ReadFile(conclusionPath)
	if err == nil {
		for _, ln := range strings.Split(string(data), "\n") {
			ln = strings.TrimSpace(strings.TrimLeft(ln, "# "))
			if ln != "" && ln != "<title>" {
				return ln
			}
		}
	}
	return branch
}

func shortTS(ts string) string { return strings.Replace(strings.TrimPrefix(ts, "2026-"), "T", " ", 1) }

func itoa(n int) string { return fmt.Sprintf("%d", n) }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// truncate/strip operate on the visible width, ignoring ANSI escapes.
func truncate(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}
