package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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

// availablePRStatusOptions returns only the status transitions that make sense
// from the PR's current state. A merged PR is terminal; a closed PR can only be
// reopened; an open/draft PR can move between the remaining states.
func availablePRStatusOptions(pr gh.PR) []string {
	if strings.EqualFold(pr.State, "MERGED") {
		return nil
	}
	if strings.EqualFold(pr.State, "CLOSED") {
		return []string{"open"}
	}
	current := "open"
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

// prStatusOverlay is the status picker popup (open / close / draft) for one
// pull request. running marks the in-flight transition; the popup swallows
// keys until prStatusDone lands.
type prStatusOverlay struct {
	pr      gh.PR
	cursor  int
	running bool
}

func (m Model) openPRStatus(pr *gh.PR) (Model, tea.Cmd) {
	if pr == nil || pr.Number <= 0 {
		return m, nil
	}
	m.overlay = prStatusOverlay{pr: *pr}
	m.status, m.notice = "", ""
	return m, nil
}

// prStatusRunning reports an in-flight status transition. The flag lives on
// the popup because the request cannot outlive it: keys are swallowed while
// it runs, and prStatusDone clears or closes the popup.
func (m Model) prStatusRunning() bool {
	o, ok := m.overlay.(prStatusOverlay)
	return ok && o.running
}

// optimisticStatus predicts the PR state after a successful transition.
// Reopening keeps draftness; only the explicit draft -> open clears it.
func optimisticStatus(pr gh.PR, target string) gh.PR {
	reopened := strings.EqualFold(pr.State, "CLOSED")
	pr.State = strings.ToUpper(target)
	switch target {
	case "draft":
		pr.State, pr.IsDraft = "OPEN", true
	case "open":
		if !reopened {
			pr.IsDraft = false
		}
	}
	return pr
}

func runPRStatus(client githubClient, pr gh.PR, target string) tea.Cmd {
	return func() tea.Msg {
		err := client.SetStatus(pr, target)
		if err == nil {
			pr = optimisticStatus(pr, target)
		}
		return prStatusDone{pr: pr, target: target, err: err}
	}
}

func (o prStatusOverlay) handleKey(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if o.running {
		return m, nil
	}
	options := availablePRStatusOptions(o.pr)
	switch msg.String() {
	case "up", "k":
		if len(options) > 0 {
			o.cursor = (o.cursor + len(options) - 1) % len(options)
		}
	case "down", "j":
		if len(options) > 0 {
			o.cursor = (o.cursor + 1) % len(options)
		}
	case "o":
		return o.submitTarget(m, "open", options)
	case "c":
		return o.submitTarget(m, "closed", options)
	case "d":
		return o.submitTarget(m, "draft", options)
	case "enter":
		if o.cursor < 0 || o.cursor >= len(options) {
			return m, nil
		}
		return o.submitTarget(m, options[o.cursor], nil)
	case "esc", "q", "n":
		m.overlay = nil
		return m, nil
	}
	m.overlay = o
	return m, nil
}

func (o prStatusOverlay) submitTarget(m Model, target string, options []string) (Model, tea.Cmd) {
	if strings.EqualFold(o.pr.State, "MERGED") {
		m.status = "merged pull requests cannot change status"
		return m, nil
	}
	if options != nil && !slices.Contains(options, target) {
		return m, nil
	}
	o.running = true
	m.overlay = o
	return m, tea.Batch(runPRStatus(m.client, o.pr, target), m.startSpinner())
}

func (m Model) handlePRStatusDone(msg prStatusDone) (Model, tea.Cmd) {
	if o, ok := m.overlay.(prStatusOverlay); ok {
		o.running = false
		m.overlay = o
	}
	if msg.err != nil {
		m.status = "PR status: " + msg.err.Error()
		return m, nil
	}
	if _, ok := m.overlay.(prStatusOverlay); ok {
		m.overlay = nil
	}
	m.notice = fmt.Sprintf("PR #%d is now %s", msg.pr.Number, msg.target)
	return m.applyPRStateChange(msg.pr)
}

// applyPRStateChange lands a state transition everywhere the PR is shown —
// detail header, navigator, loaded pages — without waiting for a refetch,
// which GitHub often answers with the old state right after a merge. Pages
// are marked stale so the next load reconciles with the server.
func (m Model) applyPRStateChange(pr gh.PR) (Model, tea.Cmd) {
	if m.cache.PR != nil && m.cache.PR.Number == pr.Number {
		*m.cache.PR = pr
	}
	m.navigator.PRs = upsertPR(m.navigator.PRs, pr)
	for key, page := range m.prList.pages {
		parts := strings.SplitN(key, ":", 3)
		state := m.prList.state
		if len(parts) > 1 {
			if value, err := strconv.Atoi(parts[1]); err == nil {
				state = prListState(value)
			}
		}
		prs := page.prs[:0]
		for _, existing := range page.prs {
			if existing.Number == pr.Number {
				if matchesListState(pr, state) {
					prs = append(prs, pr)
				}
				continue
			}
			prs = append(prs, existing)
		}
		page.prs = prs
		m.prList.pages[key] = page
	}
	m.prList.generation++
	for key, page := range m.prList.pages {
		page.fresh, page.loading = false, false
		m.prList.pages[key] = page
	}
	if m.screen == prListScreen {
		return m, m.applyPRViewState(0)
	}
	return m, m.sync()
}

func (o prStatusOverlay) render(m Model) string {
	lines := []string{stBold.Render(fmt.Sprintf("Change PR #%d status", o.pr.Number)), ""}
	for i, option := range availablePRStatusOptions(o.pr) {
		prefix := "  "
		style := stFg
		if i == o.cursor {
			prefix, style = "▸ ", stAccent.Bold(true)
		}
		lines = append(lines, prefix+style.Render(prStatusActionLabel(option)))
	}
	if strings.EqualFold(o.pr.State, "MERGED") {
		lines = append(lines, "", stMuted.Render("Merged PRs cannot be reopened."))
	} else if o.running {
		lines = append(lines, "", stAttention.Render(m.loadSpinner.View()+" updating…"))
	} else {
		lines = append(lines, "", stMuted.Render("j/k select · Enter apply · o/c/d shortcut · Esc cancel"))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(cAccent)).Padding(1, 3).Width(max(38, min(58, m.w-14))).Render(strings.Join(lines, "\n"))
}
