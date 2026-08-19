package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

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
	m.rawPending = map[string]bool{}
	m.diffCache = map[string]string{}
	m.diffPending = map[string]bool{}
	// richBodies survives: it is keyed by body content, so stale entries are
	// unused rather than wrong, and wiping it here would blank rendered
	// mermaid on refreshes whose content (and thus dispatch key) is unchanged.
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

// resolveBase recomputes the review range off the Update goroutine: the merge
// base, timeline sync, and range scans all spawn git. handleBaseResolved
// applies the result only when the range actually changed.
func (m Model) resolveBase(base string, pr *gh.PR, prURL string) tea.Cmd {
	generation := m.targetGeneration
	headRev, remote, timelinePath := m.headRev, m.remote, m.timelinePath
	var prCopy *gh.PR
	if pr != nil {
		c := *pr
		prCopy = &c
	}
	return func() tea.Msg {
		msg := baseResolved{generation: generation, prURL: prURL}
		resolved := git.ResolveBase(base)
		diffBase, newHead, reviewRange := localReviewRange(resolved, prCopy, headRev, remote)
		msg.base, msg.diffBase, msg.headRev, msg.reviewRange = resolved, diffBase, newHead, reviewRange
		if diffBase == "" {
			return msg
		}
		if !remote {
			_, _ = timeline.SyncCommits(timelinePath, diffBase)
			if events, err := event.Load(timelinePath); err == nil {
				sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })
				msg.events, msg.eventsOK = events, true
			}
		}
		msg.commits, _ = git.CommitsRange(diffBase, newHead)
		if publishedReviewHead(prCopy) == "" && !remote {
			msg.files, _ = git.ChangedFilesRange(diffBase, "")
		} else {
			msg.files, _ = git.ChangedFilesRange(diffBase, newHead)
		}
		if !remote {
			// Conflict files and the behind count go stale even when the
			// range string is unchanged: the base ref keeps moving. The
			// remote path recomputes this in fetchRemotePR instead.
			msg.readiness, msg.readinessErr = git.CheckMergeReadiness(resolved, newHead)
			msg.readinessOK = true
		}
		return msg
	}
}

func (m Model) handleBaseResolved(msg baseResolved) (Model, tea.Cmd) {
	if msg.generation != m.targetGeneration {
		return m, nil
	}
	if msg.diffBase == "" {
		return m, nil
	}
	if msg.readinessOK {
		readiness, readinessErr := msg.readiness, msg.readinessErr
		if m.cache.PR != nil {
			readiness, readinessErr = applyGitHubConflictFallback(readiness, readinessErr, *m.cache.PR)
		}
		m.mergeReadiness, m.mergeReadinessErr = readiness, readinessErr
	}
	if msg.base == m.base && msg.diffBase == m.diffBase && m.reviewRange == msg.reviewRange && m.headRev == msg.headRev {
		// Same range names, but the refs behind them move: refresh the scans
		// without dropping caches or restarting the review terminal.
		m.commits, m.files = msg.commits, msg.files
		if m.fileCursor >= len(m.files) {
			m.fileCursor = 0
		}
		return m, m.sync()
	}
	m.base, m.diffBase, m.headRev, m.reviewRange = msg.base, msg.diffBase, msg.headRev, msg.reviewRange
	m.resetDetailCaches()
	if msg.eventsOK {
		m.events = msg.events
		m.invalidateConversation()
	}
	m.commits, m.files = msg.commits, msg.files
	m.fileCursor = 0
	return m, tea.Batch(m.restartReview(m.reviewSHA, msg.prURL), m.sync())
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
	m.diffTerminal = embeddedterm.New(command, m.root, embeddedterm.Environment(m.reviewRange, m.diffBase, m.head, m.headRev, prURL, sha, m.reviewedMarksPath))
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
	// shownKey identifies the viewport content without comparing the content
	// itself: cache entries are immutable per key, so the key plus its
	// raw/rendered flavor is enough. Keyless placeholders are tiny messages,
	// so their text serves directly. Skipping identical content matters
	// because sync runs on every keystroke and background arrival, and
	// SetContent re-splits the whole diff while GotoTop would throw away the
	// reader's scroll position.
	shownKey := "raw\x00" + detail.key
	if detail.key == "" {
		shownKey = "msg\x00" + detail.raw
	}
	var cmd tea.Cmd
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
			shownKey = "rendered\x00" + key
		} else if !m.diffPending[key] {
			m.diffPending[key] = true
			cmd = renderDiff(m.targetGeneration, key, m.diffDisplay, detail.raw, m.detail.Width)
		}
	}
	if shownKey == m.detailShownKey {
		return cmd
	}
	m.detailShownKey = shownKey
	m.detail.SetContent(output)
	m.detail.GotoTop()
	return cmd
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
		title, content := "Review", m.detail.View()
		width := m.detail.Width + paneChromeW
		height := m.detail.Height + paneChromeH
		if m.reviewWide {
			width = m.w
			height = max(3, m.h-m.headerHeight()-footerLines)
		}
		if m.reviewSHA != "" {
			title = "Review · " + m.reviewSHA
		}
		if m.diffTerminal != nil && m.diffTerminal.Available() {
			content = m.diffTerminal.View(m.detail.Width, m.detail.Height)
		}
		return renderPane(title, content, width, height, m.focusDiff)
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
		if m.fileChecked(file) {
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

// loadReviewedMarks switches the in-memory marks to the given review scope.
// Each PR (or unpublished branch) owns its own persisted set, so moving
// between PRs — stacked ones included — never carries progress across.
func (m *Model) loadReviewedMarks(prNumber int, branch string) {
	m.reviewedMarksPath = store.ReviewedMarksPath(m.root, prNumber, branch)
	marks, err := store.LoadReviewedMarks(m.reviewedMarksPath)
	if err != nil {
		m.status = "reviewed marks: " + err.Error()
		marks = map[string]string{}
	}
	m.checkedFiles = marks
}

// fileChecked reports whether the file is still marked reviewed. Marks are
// keyed by path and remember the diff fingerprint they were made against, so a
// new commit only clears the files whose own diff changed — like GitHub's
// "viewed" state, which a commit elsewhere in the PR leaves alone.
func (m Model) fileChecked(file git.ChangedFile) bool {
	mark, ok := m.checkedFiles[file.Path]
	return ok && mark == file.Fingerprint
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
		m.checkedFiles = map[string]string{}
	}
	if m.fileChecked(*file) {
		delete(m.checkedFiles, file.Path)
		m.notice = "unchecked " + file.Path
	} else {
		m.checkedFiles[file.Path] = file.Fingerprint
		m.notice = "checked " + file.Path
	}
	if m.reviewedMarksPath != "" {
		if err := store.SaveReviewedMarks(m.reviewedMarksPath, m.checkedFiles); err != nil {
			m.status = "reviewed marks: " + err.Error()
		}
	}
	return m.sync()
}

func applyGitHubConflictFallback(readiness git.MergeReadiness, err error, pr gh.PR) (git.MergeReadiness, error) {
	if len(readiness.ConflictFiles) > 0 {
		return readiness, err
	}
	if strings.EqualFold(pr.Mergeable, "CONFLICTING") {
		readiness.ConflictFiles = []string{"(GitHub reports conflicts; file list unavailable)"}
	}
	return readiness, err
}

// prIsDone reports whether the bound PR is merged or closed, where base
// freshness and merge readiness no longer apply.
func (m Model) prIsDone() bool {
	return m.cache.PR != nil && (strings.EqualFold(m.cache.PR.State, "MERGED") || strings.EqualFold(m.cache.PR.State, "CLOSED"))
}

// baseFreshnessHeader summarizes how far behind base the branch is, or "" when
// the PR is already done (merged/closed), where the count is meaningless.
func (m Model) baseFreshnessHeader() string {
	if m.prIsDone() {
		return ""
	}
	switch {
	case m.mergeReadiness.Behind > 0:
		return stAttention.Render(fmt.Sprintf("⚠ out of date · %d commit%s behind base", m.mergeReadiness.Behind, plural(m.mergeReadiness.Behind)))
	case m.mergeReadinessErr != nil:
		return stMuted.Render("base freshness unavailable")
	default:
		return stGreenF.Render("✓ up to date with base")
	}
}

func (m Model) buildConflicts() (string, int) {
	header := m.baseFreshnessHeader()
	if len(m.mergeReadiness.ConflictFiles) == 0 {
		conflicts := stGreenF.Render("✓ no conflicting files")
		if m.mergeReadinessErr != nil {
			conflicts = stMuted.Render("(conflict status unavailable)")
		}
		if header == "" {
			return conflicts, 0
		}
		return header + "\n\n" + conflicts, 0
	}
	lines := make([]string, 0, len(m.mergeReadiness.ConflictFiles)+2)
	offset := 0
	if header != "" {
		lines = append(lines, header, "")
		offset = 2
	}
	for i, path := range m.mergeReadiness.ConflictFiles {
		line := stRedF.Render("⚠ ") + stFg.Render(path)
		if i == m.cursors[conflictsTab] {
			line = highlightSelectedBg(line, m.list.Width)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), m.cursors[conflictsTab] + offset
}

func (m Model) buildChecks() (string, int) {
	checkCount := 0
	if m.cache.PR != nil {
		checkCount = len(m.cache.PR.Checks)
	}
	lines := make([]string, 0, 4+checkCount)
	if header := m.baseFreshnessHeader(); header != "" {
		lines = append(lines, header)
	}
	if m.cache.PR == nil || len(m.cache.PR.Checks) == 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, stMuted.Render("(no CI checks)"))
		return strings.Join(lines, "\n"), 0
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	checksStart := len(lines)
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
		line := style.Render(icon) + " " + stFg.Render(name) + workflow
		if state != "" {
			line += stMuted.Render(" · " + strings.ToLower(strings.ReplaceAll(state, "_", " ")))
		}
		if dur := checkDuration(check); dur != "" {
			line += stMuted.Render(" · " + dur)
		}
		if i == m.cursors[checksTab] {
			line = highlightSelectedBg(line, m.list.Width)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), checksStart + m.cursors[checksTab]
}

func checkDuration(check gh.PRCheck) string {
	if check.StartedAt == "" {
		return ""
	}
	start, err := time.Parse(time.RFC3339, check.StartedAt)
	if err != nil {
		return ""
	}
	var end time.Time
	if check.CompletedAt != "" {
		end, err = time.Parse(time.RFC3339, check.CompletedAt)
		if err != nil {
			return ""
		}
	} else {
		end = time.Now()
	}
	d := end.Sub(start).Truncate(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
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
		line := style.Render(icon) + " " + stAccent.Render(c.SHA) + " " + stFg.Render(c.Subject) + stMuted.Render(" · "+relativeTS(time.Now(), c.Date))
		if i == m.cursors[commitsTab] {
			line = highlightSelectedBg(line, m.list.Width)
		}
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

type rawDetailLoaded struct {
	generation uint64
	key        string
	raw        string
}

// cachedRawDetail resolves a raw diff from the cache, or dispatches a Cmd to
// gather it: the git subprocess used to run synchronously on every cache miss,
// which happens per keystroke while browsing files or commits.
func (m *Model) cachedRawDetail(key string, load func() string) (string, bool, tea.Cmd) {
	if m.rawDetailCache == nil {
		m.rawDetailCache = map[string]string{}
	}
	if raw, ok := m.rawDetailCache[key]; ok {
		return raw, true, nil
	}
	if m.rawPending == nil {
		m.rawPending = map[string]bool{}
	}
	if m.rawPending[key] {
		return "", false, nil
	}
	m.rawPending[key] = true
	generation := m.targetGeneration
	return "", false, func() tea.Msg {
		return rawDetailLoaded{generation: generation, key: key, raw: load()}
	}
}

func (m Model) handleRawDetailLoaded(msg rawDetailLoaded) (Model, tea.Cmd) {
	// rawPending is the ticket: resetDetailCaches clears it, so results
	// computed against a discarded review range are dropped and the next
	// sync re-dispatches against the fresh state.
	if msg.generation != m.targetGeneration || !m.rawPending[msg.key] {
		return m, nil
	}
	delete(m.rawPending, msg.key)
	m.rawDetailCache[msg.key] = msg.raw
	return m, m.sync()
}

func (m *Model) loadDetail() (detailContent, tea.Cmd) {
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
			d, cached, cmd := m.cachedRawDetail(key, func() string { return git.FileDiffRange(m.diffBase, m.headRev, paths...) })
			if d != "" {
				return detailContent{key: key, raw: d, renderable: true}, nil
			}
			if !cached {
				return detailContent{raw: stMuted.Render("(loading diff…)")}, cmd
			}
		}
		return detailContent{raw: stMuted.Render("(no changes in selected file)")}, nil
	}
	key := "range:" + m.diffBase + "..." + m.headRev
	d, cached, cmd := m.cachedRawDetail(key, func() string { return git.FileDiffRange(m.diffBase, m.headRev) })
	if d != "" {
		return detailContent{key: key, raw: d, renderable: true}, nil
	}
	if !cached {
		return detailContent{raw: stMuted.Render("(loading diff…)")}, cmd
	}
	return detailContent{raw: stMuted.Render("(no changes in " + m.base + "..." + m.headRev + ")")}, nil
}

func (m *Model) loadCommitDetail(sha string) (detailContent, tea.Cmd) {
	if sha == "" {
		return detailContent{raw: stMuted.Render("no commit selected")}, nil
	}
	key := "commit:" + sha
	d, cached, cmd := m.cachedRawDetail(key, func() string { return git.Show(sha) })
	if d != "" {
		return detailContent{key: key, raw: d, renderable: true}, nil
	}
	if !cached {
		return detailContent{raw: stMuted.Render("(loading commit…)")}, cmd
	}
	return detailContent{raw: stMuted.Render("(commit " + sha + " not found in this repo)")}, nil
}
