package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/diffview"
	"github.com/shonenm/live-pr/internal/embeddedterm"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/prbody"
	"github.com/shonenm/live-pr/internal/store"
	"github.com/shonenm/live-pr/internal/timeline"
)

func (m *Model) resetDetailCaches() {
	m.rawDetailCache = map[string]string{}
	m.diffCache = map[string]string{}
	m.diffPending = map[string]bool{}
	m.richBodies = map[string]string{}
}

func (m *Model) reloadLocalConversation() {
	if m.remote {
		return
	}
	events, err := event.Load(m.timelinePath)
	if err != nil {
		m.status = "timeline: " + err.Error()
		return
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })
	m.events = events
	if dirty, err := git.HasUncommittedChanges(); err == nil {
		m.workingTreeDirty = dirty
	} else {
		m.status = "local git data: " + err.Error()
	}
	conclusion, err := os.ReadFile(store.ForBranch(m.root, m.currentBranch).Conclusion())
	if err == nil {
		m.summary = string(conclusion)
		m.title = prbody.Title(m.summary, m.currentBranch)
	} else if os.IsNotExist(err) {
		m.summary = ""
	}
	m.invalidateConversation()
}

func (m *Model) useBase(base string, pr *gh.PR, prURL string) tea.Cmd {
	base = git.ResolveBase(base)
	diffBase := localReviewBase(base, pr)
	if diffBase == "" || (base == m.base && diffBase == m.diffBase && m.reviewRange == diffBase) {
		return nil
	}
	m.base, m.diffBase, m.reviewRange = base, diffBase, diffBase
	m.resetDetailCaches()
	if !m.remote {
		_, _ = timeline.SyncCommits(m.timelinePath, diffBase)
		if events, err := event.Load(m.timelinePath); err == nil {
			m.events = events
			sort.SliceStable(m.events, func(i, j int) bool { return m.events[i].TS < m.events[j].TS })
			m.invalidateConversation()
		}
	}
	m.commits, _ = git.CommitsRange(diffBase, m.headRev)
	if m.remote {
		m.files, _ = git.ChangedFilesRange(diffBase, m.headRev)
	} else {
		m.files, _ = git.ChangedFilesRange(diffBase, "")
	}
	m.fileCursor = 0
	return m.restartReview(m.reviewSHA, prURL)
}

func (m *Model) restartReview(sha, prURL string) tea.Cmd {
	m.reviewSHA = sha
	command := m.diffCommand
	if sha != "" {
		command = m.diffCommitCommand
	}
	if m.diffTerminal != nil {
		m.diffTerminal.Close()
	}
	m.diffTerminal = embeddedterm.New(command, m.root, embeddedterm.Environment(m.reviewRange, m.diffBase, m.head, m.headRev, prURL, sha))
	m.focusDiff, m.focusExplorer = false, false
	m.layout()
	if m.diffTerminal != nil {
		return m.diffTerminal.Init()
	}
	return nil
}

func (m Model) prURL() string {
	if m.cache.PR != nil {
		return m.cache.PR.URL
	}
	return ""
}

func renderDiff(generation uint64, key, command, raw string, width int) tea.Cmd {
	return func() tea.Msg {
		out, err := diffview.Render(command, raw, width)
		return diffRendered{generation: generation, key: key, output: out, raw: raw, err: err}
	}
}

func (m *Model) syncDetail(detail detailContent) tea.Cmd {
	if strings.HasPrefix(m.status, "diff display") {
		m.status = ""
	}
	m.detailKey = ""
	output := detail.raw
	embedded := m.diffTerminal != nil && m.diffTerminal.Available()
	if !embedded && m.diffDisplay != "" && detail.renderable && detail.raw != "" {
		key := fmt.Sprintf("%s\x00%d\x00%s", m.diffDisplay, m.detail.Width, detail.key)
		m.detailKey = key
		if m.diffCache == nil {
			m.diffCache = map[string]string{}
		}
		if m.diffPending == nil {
			m.diffPending = map[string]bool{}
		}
		if cached, ok := m.diffCache[key]; ok {
			output = cached
		} else if !m.diffPending[key] {
			m.diffPending[key] = true
			m.detail.SetContent(output)
			m.detail.GotoTop()
			return renderDiff(m.targetGeneration, key, m.diffDisplay, detail.raw, m.detail.Width)
		}
	}
	m.detail.SetContent(output)
	m.detail.GotoTop()
	return nil
}

func translateDiffMouse(msg tea.MouseMsg, listWidth, detailWidth, detailHeight, headerHeight int) (tea.MouseMsg, bool) {
	contentX := listWidth + 2 // detail left border + padding
	if msg.X < contentX || msg.X >= contentX+detailWidth ||
		msg.Y < headerHeight || msg.Y >= headerHeight+detailHeight {
		return tea.MouseMsg{}, false
	}
	msg.X -= contentX
	msg.Y -= headerHeight
	return msg, true
}

// renderReviewPane frames the review side. In file-explorer mode the changed
// file list and the diff share one frame, split by an inner rule, so the two
// read as one region rather than two separate boxes.
func (m Model) renderReviewPane() string {
	if !m.fileExplorerMode() {
		title, content := "Diff", m.detail.View()
		if m.reviewSHA != "" {
			title = "Commit · " + m.reviewSHA
		}
		if m.diffTerminal != nil && m.diffTerminal.Available() {
			title, content = "Review", m.diffTerminal.View(m.detail.Width, m.detail.Height)
			if m.reviewSHA != "" {
				title = "Review · " + m.reviewSHA
			}
		}
		return renderPane(title, content, m.detail.Width+paneChromeW, m.detail.Height+paneChromeH, m.focusDiff)
	}
	title := "Files"
	if file := m.selectedFile(); file != nil {
		title = "Files · " + file.Path
	}
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(cBorder)).
		Render(strings.TrimSuffix(strings.Repeat("│\n", m.explorer.Height), "\n"))
	divider := lipgloss.NewStyle().Padding(0, 1).Render(rule)
	content := lipgloss.JoinHorizontal(lipgloss.Top, m.explorer.View(), divider, m.detail.View())
	width := m.explorer.Width + dividerW + m.detail.Width + paneChromeW
	return renderPane(title, content, width, m.explorer.Height+paneChromeH, m.focusDiff || m.focusExplorer)
}

func (m Model) buildFileExplorer() (string, int) {
	conflicts := make(map[string]bool, len(m.mergeReadiness.ConflictFiles))
	for _, path := range m.mergeReadiness.ConflictFiles {
		conflicts[path] = true
	}
	if len(m.files) == 0 {
		return stMuted.Render("Files\n(no changed files)"), 0
	}
	title := stBold.Render("Files") + stMuted.Render(fmt.Sprintf(" · %d changed", len(m.files)))
	if len(conflicts) > 0 {
		title += " · " + stRedF.Render(fmt.Sprintf("⚠ %d conflicts", len(conflicts)))
	}
	lines := []string{title}
	selectedLine := 0
	for i, file := range m.files {
		if i == m.fileCursor {
			selectedLine = len(lines)
		}
		mark := stMuted.Render("□")
		if m.checkedFiles[m.fileKey(file)] {
			mark = stGreenF.Render("✓")
		}
		path := file.Path
		if file.OldPath != "" {
			path = file.OldPath + " → " + file.Path
		}
		conflict := ""
		if conflicts[file.Path] || file.OldPath != "" && conflicts[file.OldPath] {
			conflict = stRedF.Render("⚠ ")
		}
		line := selectionBar(i == m.fileCursor) + mark + " " + conflict + fileStatusStyle(file.Status).Render(file.Status) + " " + stFg.Render(path)
		lines = append(lines, ansi.Truncate(line, max(10, m.explorer.Width), "…"))
	}
	return strings.Join(lines, "\n"), selectedLine
}

// fileStatusStyle mirrors git status letter colors: A green, D red, M yellow.
func fileStatusStyle(status string) lipgloss.Style {
	switch {
	case strings.HasPrefix(status, "A"):
		return stGreenF
	case strings.HasPrefix(status, "D"):
		return stRedF
	case strings.HasPrefix(status, "M"):
		return stAttention
	case strings.HasPrefix(status, "R"), strings.HasPrefix(status, "C"):
		return stAccent
	default:
		return stMuted
	}
}

func (m Model) selectedFile() *git.ChangedFile {
	if m.fileCursor < 0 || m.fileCursor >= len(m.files) {
		return nil
	}
	return &m.files[m.fileCursor]
}

func (m Model) fileKey(file git.ChangedFile) string {
	return m.diffBase + "..." + m.headRev + "\x00" + file.Status + "\x00" + file.OldPath + "\x00" + file.Path
}

func (m Model) fileExplorerMode() bool {
	return m.screen == detailScreen && m.diffCommand == "" && m.diffTerminal == nil
}

func (m *Model) toggleFileCheck() tea.Cmd {
	file := m.selectedFile()
	if file == nil {
		return nil
	}
	if m.checkedFiles == nil {
		m.checkedFiles = map[string]bool{}
	}
	key := m.fileKey(*file)
	m.checkedFiles[key] = !m.checkedFiles[key]
	if m.checkedFiles[key] {
		m.notice = "checked " + file.Path
	} else {
		m.notice = "unchecked " + file.Path
	}
	return m.sync()
}

func applyGitHubConflictFallback(readiness git.MergeReadiness, err error, pr gh.PR) (git.MergeReadiness, error) {
	if len(readiness.ConflictFiles) > 0 {
		return readiness, err
	}
	if strings.EqualFold(pr.Mergeable, "CONFLICTING") || strings.EqualFold(pr.MergeStateStatus, "DIRTY") {
		readiness.ConflictFiles = []string{"(GitHub reports conflicts; file list unavailable)"}
	}
	return readiness, err
}

func (m Model) buildConflicts() (string, int) {
	if len(m.mergeReadiness.ConflictFiles) == 0 {
		if m.mergeReadinessErr != nil {
			return stMuted.Render("(conflict status unavailable)"), 0
		}
		return stGreenF.Render("✓ no conflicting files"), 0
	}
	lines := make([]string, 0, len(m.mergeReadiness.ConflictFiles))
	for i, path := range m.mergeReadiness.ConflictFiles {
		lines = append(lines, selectionBar(i == m.cursors[conflictsTab])+stRedF.Render("⚠ ")+stFg.Render(path))
	}
	return strings.Join(lines, "\n"), m.cursors[conflictsTab]
}

func (m Model) buildChecks() (string, int) {
	checkCount := 0
	if m.cache.PR != nil {
		checkCount = len(m.cache.PR.Checks)
	}
	lines := make([]string, 0, 4+checkCount)
	behind := m.mergeReadiness.Behind
	switch {
	case behind > 0:
		lines = append(lines, stAttention.Render(fmt.Sprintf("⚠ out of date · %d commit%s behind base", behind, plural(behind))))
	case m.mergeReadinessErr != nil:
		lines = append(lines, stMuted.Render("base freshness unavailable"))
	default:
		lines = append(lines, stGreenF.Render("✓ up to date with base"))
	}
	if m.cache.PR == nil || len(m.cache.PR.Checks) == 0 {
		lines = append(lines, "", stMuted.Render("(no CI checks)"))
		return strings.Join(lines, "\n"), 0
	}
	lines = append(lines, "")
	for i, check := range m.cache.PR.Checks {
		icon, _, style := commitCIStatus(checkRollupState([]gh.PRCheck{check}))
		name := check.Name
		if name == "" {
			name = check.Context
		}
		if name == "" {
			name = "unnamed check"
		}
		workflow := ""
		if check.WorkflowName != "" && check.WorkflowName != name {
			workflow = stMuted.Render(" · " + check.WorkflowName)
		}
		state := check.Conclusion
		if state == "" {
			state = check.Status
		}
		if state == "" {
			state = check.State
		}
		line := selectionBar(i == m.cursors[checksTab]) + style.Render(icon) + " " + stFg.Render(name) + workflow
		if state != "" {
			line += stMuted.Render(" · " + strings.ToLower(strings.ReplaceAll(state, "_", " ")))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), m.cursors[checksTab]
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (m Model) buildCommits() (string, int) {
	if len(m.commits) == 0 {
		return stMuted.Render("(no commits in " + m.base + "..HEAD)"), 0
	}
	lines := make([]string, 0, len(m.commits))
	ciStates := m.commitCIStates()
	for i, c := range m.commits {
		icon, style := "●", stMuted
		if m.cache.PR != nil {
			icon, _, style = commitCIStatus(ciStates[c.SHA])
		}
		line := selectionBar(i == m.cursors[commitsTab]) + style.Render(icon) + " " + stAccent.Render(c.SHA) + " " + stFg.Render(c.Subject) + stMuted.Render(" · "+shortTS(c.Date))
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), m.cursors[commitsTab]
}

func (m Model) commitCIStates() map[string]string {
	states := make(map[string]string, len(m.commits))
	if m.cache.PR == nil {
		return states
	}
	lengths := map[int]bool{}
	for _, commit := range m.commits {
		lengths[len(commit.SHA)] = true
	}
	for _, commit := range m.cache.PR.Commits {
		for length := range lengths {
			if len(commit.OID) >= length {
				prefix := commit.OID[:length]
				states[prefix] = commit.CheckRollupState
			}
		}
	}
	return states
}

func commitCIStatus(state string) (string, string, lipgloss.Style) {
	switch strings.ToUpper(state) {
	case "SUCCESS":
		return "✓", "CI passed", stGreenF
	case "FAILURE", "ERROR":
		return "✗", "CI failed", stRedF
	case "PENDING", "EXPECTED", "IN_PROGRESS":
		return "◐", "CI pending", stAttention
	default:
		return "○", "CI unavailable", stMuted
	}
}

func (m *Model) cachedRawDetail(key string, load func() string) string {
	if m.rawDetailCache == nil {
		m.rawDetailCache = map[string]string{}
	}
	if raw, ok := m.rawDetailCache[key]; ok {
		return raw
	}
	raw := load()
	m.rawDetailCache[key] = raw
	return raw
}

func (m *Model) loadDetail() detailContent {
	if m.reviewSHA != "" {
		return m.loadCommitDetail(m.reviewSHA)
	}
	if m.fileExplorerMode() {
		if file := m.selectedFile(); file != nil {
			paths := []string{file.Path}
			if file.OldPath != "" {
				paths = append(paths, file.OldPath)
			}
			key := fmt.Sprintf("file:%s...%s:%s:%s:%s", m.diffBase, m.headRev, file.Status, file.OldPath, file.Path)
			if d := m.cachedRawDetail(key, func() string { return git.FileDiffRange(m.diffBase, m.headRev, paths...) }); d != "" {
				return detailContent{key: key, raw: d, renderable: true}
			}
		}
		return detailContent{raw: stMuted.Render("(no changes in selected file)")}
	}
	key := "range:" + m.diffBase + "..." + m.headRev
	if d := m.cachedRawDetail(key, func() string { return git.FileDiffRange(m.diffBase, m.headRev) }); d != "" {
		return detailContent{key: key, raw: d, renderable: true}
	}
	return detailContent{raw: stMuted.Render("(no changes in " + m.base + "..." + m.headRev + ")")}
}

func (m *Model) loadCommitDetail(sha string) detailContent {
	if sha == "" {
		return detailContent{raw: stMuted.Render("no commit selected")}
	}
	key := "commit:" + sha
	if d := m.cachedRawDetail(key, func() string { return git.Show(sha) }); d != "" {
		return detailContent{key: key, raw: d, renderable: true}
	}
	return detailContent{raw: stMuted.Render("(commit " + sha + " not found in this repo)")}
}
