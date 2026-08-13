package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	gh "github.com/shonenm/live-pr/internal/github"
)

var prStatusOptions = []string{"open", "closed", "draft"}

// prStatusActionLabel is the label shown in the picker. The option value stays
// the resulting GitHub state ("closed"), but the action reads as "Close".
func prStatusActionLabel(option string) string {
	if option == "closed" {
		return "Close"
	}
	return strings.ToUpper(option[:1]) + option[1:]
}

func availablePRStatusOptions(pr gh.PR) []string {
	current := strings.ToLower(pr.State)
	if pr.IsDraft {
		current = "draft"
	}
	options := make([]string, 0, len(prStatusOptions)-1)
	for _, option := range prStatusOptions {
		if option != current {
			options = append(options, option)
		}
	}
	return options
}

func (m Model) openPRStatus(pr *gh.PR) (Model, tea.Cmd) {
	if pr == nil || pr.Number <= 0 {
		return m, nil
	}
	m.statusPR, m.statusCursor = *pr, 0
	m.status, m.notice = "", ""
	return m, nil
}

func runPRStatus(pr gh.PR, target string) tea.Cmd {
	return func() tea.Msg {
		err := gh.New().SetStatus(pr, target)
		if err == nil {
			pr.State, pr.IsDraft = strings.ToUpper(target), target == "draft"
			if target == "draft" {
				pr.State = "OPEN"
			}
		}
		return prStatusDone{pr: pr, target: target, err: err}
	}
}

func (m Model) handlePRStatusKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.statusRunning {
		return m, nil
	}
	options := availablePRStatusOptions(m.statusPR)
	switch msg.String() {
	case "up", "k":
		m.statusCursor = (m.statusCursor + len(options) - 1) % len(options)
	case "down", "j":
		m.statusCursor = (m.statusCursor + 1) % len(options)
	case "o":
		return m.submitPRStatusTarget("open", options)
	case "c":
		return m.submitPRStatusTarget("closed", options)
	case "d":
		return m.submitPRStatusTarget("draft", options)
	case "enter":
		return m.submitPRStatus()
	case "esc", "q", "n":
		m.statusPR = gh.PR{}
	}
	return m, nil
}

func (m Model) submitPRStatus() (Model, tea.Cmd) {
	return m.submitPRStatusTarget(availablePRStatusOptions(m.statusPR)[m.statusCursor], nil)
}

func (m Model) submitPRStatusTarget(target string, options []string) (Model, tea.Cmd) {
	if strings.EqualFold(m.statusPR.State, "MERGED") {
		m.status = "merged pull requests cannot change status"
		return m, nil
	}
	if options != nil && !slices.Contains(options, target) {
		return m, nil
	}
	m.statusRunning = true
	return m, tea.Batch(runPRStatus(m.statusPR, target), m.startSpinner())
}

func (m Model) handlePRStatusDone(msg prStatusDone) (Model, tea.Cmd) {
	m.statusRunning = false
	if msg.err != nil {
		m.status = "PR status: " + msg.err.Error()
		return m, nil
	}
	m.statusPR = gh.PR{}
	m.notice = fmt.Sprintf("PR #%d is now %s", msg.pr.Number, msg.target)
	if m.screen == detailScreen && m.cache.PR != nil && m.cache.PR.Number == msg.pr.Number {
		*m.cache.PR = msg.pr
	}
	m.navigator.PRs = upsertPR(m.navigator.PRs, msg.pr)
	for key, page := range m.prPages {
		parts := strings.SplitN(key, ":", 3)
		state := m.prListState
		if len(parts) > 1 {
			if value, err := strconv.Atoi(parts[1]); err == nil {
				state = prListState(value)
			}
		}
		prs := page.prs[:0]
		for _, pr := range page.prs {
			if pr.Number == msg.pr.Number {
				if matchesListState(msg.pr, state) {
					prs = append(prs, msg.pr)
				}
				continue
			}
			prs = append(prs, pr)
		}
		page.prs = prs
		m.prPages[key] = page
	}
	m.prListGeneration++
	for key, page := range m.prPages {
		page.fresh, page.loading = false, false
		m.prPages[key] = page
	}
	if m.screen == prListScreen {
		return m, m.applyPRViewState(0)
	}
	return m, m.sync()
}

func (m Model) renderPRStatusPopup() string {
	lines := []string{stBold.Render(fmt.Sprintf("Change PR #%d status", m.statusPR.Number)), ""}
	for i, option := range availablePRStatusOptions(m.statusPR) {
		prefix := "  "
		style := stFg
		if i == m.statusCursor {
			prefix, style = "▸ ", stAccent.Bold(true)
		}
		lines = append(lines, prefix+style.Render(prStatusActionLabel(option)))
	}
	if strings.EqualFold(m.statusPR.State, "MERGED") {
		lines = append(lines, "", stMuted.Render("Merged PRs cannot be reopened."))
	} else if m.statusRunning {
		lines = append(lines, "", stAttention.Render(m.loadSpinner.View()+" updating…"))
	} else {
		lines = append(lines, "", stMuted.Render("j/k select · Enter apply · o/c/d shortcut · Esc cancel"))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(cAccent)).Padding(1, 3).Width(max(38, min(58, m.w-14))).Render(strings.Join(lines, "\n"))
}
