package tui

import (
	"crypto/sha256"
	"fmt"
	"hash/maphash"
	"os"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

// detailFocus identifies which detail-screen pane owns keyboard input: the
// conversation pane, the review pane (embedded reviewer or static diff), or
// the file explorer. reviewWide stays a separate detailModel flag because
// full-width layout is orthogonal to which pane has focus.
type detailFocus uint8

const (
	focusConversation detailFocus = iota
	focusReview
	focusExplorer
)

// detailModel groups the state that only the detail screen touches: the
// loaded target (title, refs, commits, files, events, merge readiness), the
// tab/cursor/focus selection, the raw-diff and rendered-diff caches, reviewed
// marks, and the conversation render caches. Shared state stays on Model —
// the detail viewport (the list screen's preview pane renders through it),
// remote (read by the list's current-PR marker via isCurrentTargetPR),
// targetGeneration (bumped by list-side navigation and checkout), the diff
// config, and the diff terminal (torn down by Model-level target switches).
type detailModel struct {
	title             string
	summary           string
	base, head        string
	diffBase, headRev string
	reviewRange       string
	reviewSHA         string
	events            []event.Event
	commits           []git.Commit
	files             []git.ChangedFile
	mergeReadiness    git.MergeReadiness
	mergeReadinessErr error

	active     tab
	cursors    [tabCount]int
	listAnchor listAnchor

	focus             detailFocus
	reviewWide        bool
	fileCursor        int
	diffKey           string
	shownKey          string
	rawCache          map[string]string
	rawErrs           map[string]string
	rawPending        map[string]bool
	diffCache         map[string]string
	diffPending       map[string]bool
	checkedFiles      map[string]string
	reviewedMarksPath string

	conversationCache       []conversationItem
	conversationDirty       bool
	conversationRender      string
	conversationRenderLine  int
	conversationRenderKey   convRenderKey
	conversationRenderValid bool
	conversationRows        [][2]int // per-item [start, end) line ranges of the cached render
	convItemCache           map[string][]string
	richBodies              map[string]string
	lastRichContentKey      [sha256.Size]byte
	commitsRenderKey        tabRenderKey
	commitsRender           string
	commitsRenderLine       int
	commitsRenderValid      bool
	checksRenderKey         tabRenderKey
	checksRender            string
	checksRenderLine        int
	checksRenderValid       bool
}

// tabRenderKey caches the commits and checks tab renders the way convRenderKey
// caches the conversation: the cached string is reused when the key matches on
// access and rebuilt otherwise, with no explicit invalidation. content
// fingerprints every input the rows render from, so commitCIStates and the
// per-row styling run only when the underlying data actually changed.
type tabRenderKey struct {
	cursor  int
	width   int
	content uint64
}

// tabRenderSeed only needs to be stable within the process: the fingerprints
// live in in-memory caches and are never persisted. maphash hashes strings
// without the per-field allocations a crypto or fnv hash would need.
var tabRenderSeed = maphash.MakeSeed()

func fingerprintStrings(h *maphash.Hash, fields ...string) {
	for _, field := range fields {
		_, _ = h.WriteString(field)
		_ = h.WriteByte(0)
	}
}

func (d *detailModel) resetCaches() {
	d.rawCache = map[string]string{}
	d.rawErrs = map[string]string{}
	d.rawPending = map[string]bool{}
	d.diffCache = map[string]string{}
	d.diffPending = map[string]bool{}
	// richBodies survives: it is keyed by body content, so stale entries are
	// unused rather than wrong, and wiping it here would blank rendered
	// mermaid on refreshes whose content (and thus dispatch key) is unchanged.
	// Switching to a different target prunes it via pruneRichContent instead.
}

// pruneRichContent drops the rendered-mermaid cache when the detail target
// switches to a different PR: entries are keyed by full body text, so another
// PR's bodies are never looked up again and only accumulate memory across a
// session. Same-target reloads must not call this — resetCaches keeps
// richBodies precisely so an unchanged conversation reuses its diagrams.
// Zeroing the dispatch key makes dispatchRichContent re-render if the same
// content is ever opened again.
func (d *detailModel) pruneRichContent() {
	d.richBodies = map[string]string{}
	d.lastRichContentKey = [sha256.Size]byte{}
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
	m.detailView.events = events
	if oid, err := git.Revision("HEAD"); err == nil {
		m.localHeadOID = oid
	}
	if summary, err := git.WorktreeStatus(); err == nil {
		m.worktreeSummary, m.workingTreeDirty = summary, summary.Total() > 0
	} else {
		m.status = "local git data: " + err.Error()
	}
	conclusion, err := os.ReadFile(store.ForBranch(m.root, m.currentBranch).Conclusion())
	if err == nil {
		m.detailView.summary = string(conclusion)
		m.detailView.title = prbody.Title(m.detailView.summary, m.currentBranch)
	} else if os.IsNotExist(err) {
		m.detailView.summary = ""
	}
	m.detailView.invalidateConversation()
}

// resolveBase recomputes the review range off the Update goroutine: the merge
// base, timeline sync, and range scans all spawn git. handleBaseResolved
// applies the result only when the range actually changed.
func (m Model) resolveBase(base string, pr *gh.PR, prURL string) tea.Cmd {
	generation := m.targetGeneration
	headRev, remote, timelinePath := m.detailView.headRev, m.remote, m.timelinePath
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
		if !remote {
			msg.files, _ = git.ChangedFilesRange(diffBase, "")
			msg.localHeadOID, _ = git.Revision("HEAD")
			if prCopy != nil && prCopy.HeadRefOID != "" {
				msg.revisionRelation, _ = git.CompareRevisions("HEAD", prCopy.HeadRefOID)
				if msg.revisionRelation == git.RevisionSynced || msg.revisionRelation == git.RevisionLocalAhead {
					if localOnly, err := git.CommitsRange(prCopy.HeadRefOID, "HEAD"); err == nil {
						msg.publishedCommits = max(0, len(msg.commits)-len(localOnly))
					}
				} else if msg.revisionRelation == git.RevisionDiverged {
					msg.localDiverged = true
				}
			}
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
		m.detailView.mergeReadiness, m.detailView.mergeReadinessErr = readiness, readinessErr
	}
	if msg.base == m.detailView.base && msg.diffBase == m.detailView.diffBase && m.detailView.reviewRange == msg.reviewRange && m.detailView.headRev == msg.headRev {
		// Same range names, but the refs behind them move: refresh the scans
		// without dropping caches or restarting the review terminal.
		m.detailView.commits, m.detailView.files = msg.commits, msg.files
		if !m.remote {
			m.localHeadOID, m.revisionRelation = msg.localHeadOID, msg.revisionRelation
			m.publishedCommits, m.localDiverged = msg.publishedCommits, msg.localDiverged
		}
		if m.detailView.fileCursor >= len(m.detailView.files) {
			m.detailView.fileCursor = 0
		}
		return m, m.sync()
	}
	m.detailView.base, m.detailView.diffBase, m.detailView.headRev, m.detailView.reviewRange = msg.base, msg.diffBase, msg.headRev, msg.reviewRange
	m.detailView.resetCaches()
	if msg.eventsOK {
		m.detailView.events = msg.events
		m.detailView.invalidateConversation()
	}
	m.detailView.commits, m.detailView.files = msg.commits, msg.files
	if !m.remote {
		m.localHeadOID, m.revisionRelation = msg.localHeadOID, msg.revisionRelation
		m.publishedCommits, m.localDiverged = msg.publishedCommits, msg.localDiverged
	}
	m.detailView.fileCursor = 0
	return m, tea.Batch(m.restartReview(m.detailView.reviewSHA, msg.prURL), m.sync())
}

func (m *Model) restartReview(sha, prURL string) tea.Cmd {
	m.detailView.reviewSHA = sha
	command := m.diffCommand
	if sha != "" {
		command = m.diffCommitCommand
	}
	if m.diffTerminal != nil {
		m.diffTerminal.Close()
	}
	m.diffTerminal = embeddedterm.New(command, m.root, embeddedterm.Environment(m.detailView.reviewRange, m.detailView.diffBase, m.detailView.head, m.detailView.headRev, prURL, sha, m.detailView.reviewedMarksPath))
	m.detailView.focus = focusConversation
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
	m.detailView.diffKey = ""
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
		key := fmt.Sprintf("%s\x00%d\x00%s", m.diffDisplay, m.detail.Width(), detail.key)
		m.detailView.diffKey = key
		if m.detailView.diffCache == nil {
			m.detailView.diffCache = map[string]string{}
		}
		if m.detailView.diffPending == nil {
			m.detailView.diffPending = map[string]bool{}
		}
		if cached, ok := m.detailView.diffCache[key]; ok {
			output = cached
			shownKey = "rendered\x00" + key
		} else if !m.detailView.diffPending[key] {
			m.detailView.diffPending[key] = true
			cmd = renderDiff(m.targetGeneration, key, m.diffDisplay, detail.raw, m.detail.Width())
		}
	}
	if shownKey == m.detailView.shownKey {
		return cmd
	}
	m.detailView.shownKey = shownKey
	m.detail.SetContent(output)
	m.detail.GotoTop()
	return cmd
}

func translateDiffMouse(msg tea.MouseMsg, listWidth, detailWidth, detailHeight, headerHeight int) (tea.MouseMsg, bool) {
	mouse := msg.Mouse()
	contentX := listWidth + 2 // detail left border + padding
	if mouse.X < contentX || mouse.X >= contentX+detailWidth ||
		mouse.Y < headerHeight || mouse.Y >= headerHeight+detailHeight {
		return nil, false
	}
	mouse.X -= contentX
	mouse.Y -= headerHeight
	switch msg.(type) {
	case tea.MouseClickMsg:
		return tea.MouseClickMsg(mouse), true
	case tea.MouseReleaseMsg:
		return tea.MouseReleaseMsg(mouse), true
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg(mouse), true
	case tea.MouseMotionMsg:
		return tea.MouseMotionMsg(mouse), true
	}
	return nil, false
}

// renderReviewPane frames the review side. In file-explorer mode the changed
// file list and the diff share one frame, split by an inner rule, so the two
// read as one region rather than two separate boxes.
func (m Model) renderReviewPane() string {
	if !m.fileExplorerMode() {
		title, content := "Review", m.detail.View()
		width := m.detail.Width() + paneChromeW
		height := m.detail.Height() + paneChromeH
		if m.detailView.reviewWide {
			width = m.w
			height = max(3, m.h-m.headerHeight()-footerLines)
		}
		if m.detailView.reviewSHA != "" {
			title = "Review · " + m.detailView.reviewSHA
		}
		if m.diffTerminal != nil && m.diffTerminal.Available() {
			content = m.diffTerminal.View(m.detail.Width(), m.detail.Height())
		}
		return renderPane(title, content, width, height, m.detailView.focus == focusReview)
	}
	title := "Files"
	if file := m.detailView.selectedFile(); file != nil {
		title = "Files · " + file.Path
	}
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(cBorder)).
		Render(strings.TrimSuffix(strings.Repeat("│\n", m.explorer.Height()), "\n"))
	divider := lipgloss.NewStyle().Padding(0, 1).Render(rule)
	content := lipgloss.JoinHorizontal(lipgloss.Top, m.explorer.View(), divider, m.detail.View())
	width := m.explorer.Width() + dividerW + m.detail.Width() + paneChromeW
	return renderPane(title, content, width, m.explorer.Height()+paneChromeH, m.detailView.focus != focusConversation)
}

func (m Model) buildFileExplorer() (string, int) {
	conflicts := make(map[string]bool, len(m.detailView.mergeReadiness.ConflictFiles))
	for _, path := range m.detailView.mergeReadiness.ConflictFiles {
		conflicts[path] = true
	}
	if len(m.detailView.files) == 0 {
		return stMuted.Render("Files\n(no changed files)"), 0
	}
	title := stBold.Render("Files") + stMuted.Render(fmt.Sprintf(" · %d changed", len(m.detailView.files)))
	if len(conflicts) > 0 {
		title += " · " + stRedF.Render(fmt.Sprintf("⚠ %d conflicts", len(conflicts)))
	}
	lines := []string{title}
	selectedLine := 0
	for i, file := range m.detailView.files {
		if i == m.detailView.fileCursor {
			selectedLine = len(lines)
		}
		mark := stMuted.Render("□")
		if m.detailView.fileChecked(file) {
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
		line := selectionBar(i == m.detailView.fileCursor) + mark + " " + conflict + fileStatusStyle(file.Status).Render(file.Status) + " " + stFg.Render(path)
		lines = append(lines, ansi.Truncate(line, max(10, m.explorer.Width()), "…"))
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

func (d detailModel) selectedFile() *git.ChangedFile {
	if d.fileCursor < 0 || d.fileCursor >= len(d.files) {
		return nil
	}
	return &d.files[d.fileCursor]
}

// loadReviewedMarks switches the in-memory marks to the given review scope.
// Each PR (or unpublished branch) owns its own persisted set, so moving
// between PRs — stacked ones included — never carries progress across.
func (m *Model) loadReviewedMarks(prNumber int, branch string) {
	m.detailView.reviewedMarksPath = store.ReviewedMarksPath(m.root, prNumber, branch)
	marks, err := store.LoadReviewedMarks(m.detailView.reviewedMarksPath)
	if err != nil {
		m.status = "reviewed marks: " + err.Error()
		marks = map[string]string{}
	}
	m.detailView.checkedFiles = marks
}

// fileChecked reports whether the file is still marked reviewed. Marks are
// keyed by path and remember the diff fingerprint they were made against, so a
// new commit only clears the files whose own diff changed — like GitHub's
// "viewed" state, which a commit elsewhere in the PR leaves alone.
func (d detailModel) fileChecked(file git.ChangedFile) bool {
	mark, ok := d.checkedFiles[file.Path]
	return ok && mark == file.Fingerprint
}

func (m Model) fileExplorerMode() bool {
	return m.screen == detailScreen && m.diffCommand == "" && m.diffTerminal == nil
}

func (m *Model) toggleFileCheck() tea.Cmd {
	file := m.detailView.selectedFile()
	if file == nil {
		return nil
	}
	if m.detailView.checkedFiles == nil {
		m.detailView.checkedFiles = map[string]string{}
	}
	if m.detailView.fileChecked(*file) {
		delete(m.detailView.checkedFiles, file.Path)
		m.notice = "unchecked " + file.Path
	} else {
		m.detailView.checkedFiles[file.Path] = file.Fingerprint
		m.notice = "checked " + file.Path
	}
	if m.detailView.reviewedMarksPath != "" {
		if err := store.SaveReviewedMarks(m.detailView.reviewedMarksPath, m.detailView.checkedFiles); err != nil {
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
	case m.detailView.mergeReadiness.Behind > 0:
		return stAttention.Render(fmt.Sprintf("⚠ out of date · %d commit%s behind base", m.detailView.mergeReadiness.Behind, plural(m.detailView.mergeReadiness.Behind)))
	case m.detailView.mergeReadinessErr != nil:
		return stMuted.Render("base freshness unavailable")
	default:
		return stGreenF.Render("✓ up to date with base")
	}
}

func (m Model) buildConflicts() (string, int) {
	header := m.baseFreshnessHeader()
	if len(m.detailView.mergeReadiness.ConflictFiles) == 0 {
		conflicts := stGreenF.Render("✓ no conflicting files")
		if m.detailView.mergeReadinessErr != nil {
			conflicts = stMuted.Render("(conflict status unavailable)")
		}
		if header == "" {
			return conflicts, 0
		}
		return header + "\n\n" + conflicts, 0
	}
	lines := make([]string, 0, len(m.detailView.mergeReadiness.ConflictFiles)+2)
	offset := 0
	if header != "" {
		lines = append(lines, header, "")
		offset = 2
	}
	for i, path := range m.detailView.mergeReadiness.ConflictFiles {
		line := stRedF.Render("⚠ ") + stFg.Render(path)
		if i == m.detailView.cursors[conflictsTab] {
			line = highlightSelectedBg(line, m.list.Width())
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), m.detailView.cursors[conflictsTab] + offset
}

// checksFingerprint hashes the freshness header plus every check field the
// rows render, so the cached render survives syncs whose data is unchanged.
func (m *Model) checksFingerprint(header string) uint64 {
	var h maphash.Hash
	h.SetSeed(tabRenderSeed)
	fingerprintStrings(&h, header)
	for _, check := range m.cache.PR.Checks {
		fingerprintStrings(&h, check.Name, check.Context, check.WorkflowName, check.Status, check.Conclusion, check.State, check.StartedAt, check.CompletedAt)
	}
	return h.Sum64()
}

func (m *Model) buildChecks() (string, int) {
	header := m.baseFreshnessHeader()
	if m.cache.PR == nil || len(m.cache.PR.Checks) == 0 {
		lines := make([]string, 0, 3)
		if header != "" {
			lines = append(lines, header, "")
		}
		lines = append(lines, stMuted.Render("(no CI checks)"))
		return strings.Join(lines, "\n"), 0
	}
	key := tabRenderKey{cursor: m.detailView.cursors[checksTab], width: m.list.Width(), content: m.checksFingerprint(header)}
	if m.detailView.checksRenderValid && m.detailView.checksRenderKey == key {
		return m.detailView.checksRender, m.detailView.checksRenderLine
	}
	lines := make([]string, 0, 2+len(m.cache.PR.Checks))
	if header != "" {
		lines = append(lines, header, "")
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
		if i == m.detailView.cursors[checksTab] {
			line = highlightSelectedBg(line, m.list.Width())
		}
		lines = append(lines, line)
	}
	out, selected := strings.Join(lines, "\n"), checksStart+m.detailView.cursors[checksTab]
	m.detailView.checksRender, m.detailView.checksRenderLine = out, selected
	m.detailView.checksRenderKey, m.detailView.checksRenderValid = key, true
	return out, selected
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

// commitsFingerprint hashes everything a commit row renders from: the local
// commit list plus the PR commit rollups feeding commitCIStates. Hashing is
// O(rows) but two orders of magnitude cheaper than restyling every row.
func (m *Model) commitsFingerprint() uint64 {
	var h maphash.Hash
	h.SetSeed(tabRenderSeed)
	fingerprintStrings(&h, fmt.Sprint(m.publishedCommits), fmt.Sprint(m.localDiverged), fmt.Sprint(m.remote), fmt.Sprint(m.worktreeSummary))
	for _, c := range m.detailView.commits {
		fingerprintStrings(&h, c.SHA, c.Subject, c.Date)
	}
	if m.cache.PR != nil {
		_ = h.WriteByte(1)
		for _, c := range m.cache.PR.Commits {
			fingerprintStrings(&h, c.OID, c.CheckRollupState)
		}
	}
	return h.Sum64()
}

func (m *Model) buildCommits() (string, int) {
	showWorktree := !m.remote && m.worktreeSummary.Total() > 0
	if len(m.detailView.commits) == 0 && !showWorktree {
		return stMuted.Render("(no commits in " + m.detailView.base + "..HEAD)"), 0
	}
	key := tabRenderKey{cursor: m.detailView.cursors[commitsTab], width: m.list.Width(), content: m.commitsFingerprint()}
	if m.detailView.commitsRenderValid && m.detailView.commitsRenderKey == key {
		return m.detailView.commitsRender, m.detailView.commitsRenderLine
	}
	lines := make([]string, 0, len(m.detailView.commits)+4)
	ciStates := m.commitCIStates()
	published := m.publishedCommits
	if m.remote {
		published = len(m.detailView.commits)
	}
	if published > len(m.detailView.commits) {
		published = len(m.detailView.commits)
	}
	selectedLine := 0
	for i, c := range m.detailView.commits {
		if i == 0 && published > 0 {
			lines = append(lines, stBold.Render("Published on PR"))
		}
		if i == published && published < len(m.detailView.commits) {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			heading := "Local only"
			if m.localDiverged {
				heading = "Local history diverged from PR head"
			}
			lines = append(lines, stAttention.Render(heading))
		}
		icon, style := "●", stMuted
		if i < published && m.cache.PR != nil {
			icon, _, style = commitCIStatus(ciStates[c.SHA])
		}
		line := style.Render(icon) + " " + stAccent.Render(c.SHA) + " " + stFg.Render(c.Subject) + stMuted.Render(" · "+relativeTS(time.Now(), c.Date))
		if i == m.detailView.cursors[commitsTab] {
			selectedLine = len(lines)
			line = highlightSelectedBg(line, m.list.Width())
		}
		lines = append(lines, line)
	}
	if showWorktree {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, stAttention.Render("Working tree"))
		s := m.worktreeSummary
		parts := make([]string, 0, 3)
		if s.Staged > 0 {
			parts = append(parts, fmt.Sprintf("%d staged", s.Staged))
		}
		if s.Unstaged > 0 {
			parts = append(parts, fmt.Sprintf("%d unstaged", s.Unstaged))
		}
		if s.Untracked > 0 {
			parts = append(parts, fmt.Sprintf("%d untracked", s.Untracked))
		}
		line := stAttention.Render("●") + " " + stFg.Render(strings.Join(parts, " · "))
		if m.detailView.cursors[commitsTab] == len(m.detailView.commits) {
			selectedLine = len(lines)
			line = highlightSelectedBg(line, m.list.Width())
		}
		lines = append(lines, line)
	}
	out := strings.Join(lines, "\n")
	m.detailView.commitsRender, m.detailView.commitsRenderLine = out, selectedLine
	m.detailView.commitsRenderKey, m.detailView.commitsRenderValid = key, true
	return out, m.detailView.commitsRenderLine
}

func (m Model) commitCIStates() map[string]string {
	states := make(map[string]string, len(m.detailView.commits))
	if m.cache.PR == nil {
		return states
	}
	lengths := map[int]bool{}
	for _, commit := range m.detailView.commits {
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
	err        string // first line of the load error, "" on success
}

// cachedRawDetail resolves a raw diff from the cache, or dispatches a Cmd to
// gather it: the git subprocess used to run synchronously on every cache miss,
// which happens per keystroke while browsing files or commits. loadErr is the
// cached load failure for key, so callers can tell "git failed" from "the
// diff really is empty".
func (m *Model) cachedRawDetail(key string, load func() (string, error)) (raw, loadErr string, cached bool, cmd tea.Cmd) {
	if m.detailView.rawCache == nil {
		m.detailView.rawCache = map[string]string{}
	}
	if m.detailView.rawErrs == nil {
		m.detailView.rawErrs = map[string]string{}
	}
	if raw, ok := m.detailView.rawCache[key]; ok {
		return raw, m.detailView.rawErrs[key], true, nil
	}
	if m.detailView.rawPending == nil {
		m.detailView.rawPending = map[string]bool{}
	}
	if m.detailView.rawPending[key] {
		return "", "", false, nil
	}
	m.detailView.rawPending[key] = true
	generation := m.targetGeneration
	return "", "", false, func() tea.Msg {
		raw, err := load()
		msg := rawDetailLoaded{generation: generation, key: key, raw: raw}
		if err != nil {
			msg.err = strings.TrimSpace(strings.SplitN(err.Error(), "\n", 2)[0])
		}
		return msg
	}
}

func (m Model) handleRawDetailLoaded(msg rawDetailLoaded) (Model, tea.Cmd) {
	// rawPending is the ticket: resetCaches clears it, so results
	// computed against a discarded review range are dropped and the next
	// sync re-dispatches against the fresh state.
	if msg.generation != m.targetGeneration || !m.detailView.rawPending[msg.key] {
		return m, nil
	}
	delete(m.detailView.rawPending, msg.key)
	m.detailView.rawCache[msg.key] = msg.raw
	if msg.err != "" {
		if m.detailView.rawErrs == nil {
			m.detailView.rawErrs = map[string]string{}
		}
		m.detailView.rawErrs[msg.key] = msg.err
	}
	return m, m.sync()
}

func (m *Model) loadDetail() (detailContent, tea.Cmd) {
	if m.detailView.reviewSHA != "" {
		return m.loadCommitDetail(m.detailView.reviewSHA)
	}
	if m.fileExplorerMode() {
		if file := m.detailView.selectedFile(); file != nil {
			paths := []string{file.Path}
			if file.OldPath != "" {
				paths = append(paths, file.OldPath)
			}
			key := fmt.Sprintf("file:%s...%s:%s:%s:%s", m.detailView.diffBase, m.detailView.headRev, file.Status, file.OldPath, file.Path)
			head := m.detailView.headRev
			if !m.remote {
				head = ""
			}
			d, loadErr, cached, cmd := m.cachedRawDetail(key, func() (string, error) {
				return git.FileDiffRange(m.detailView.diffBase, head, paths...)
			})
			if loadErr != "" {
				return detailContent{raw: stMuted.Render("(diff unavailable: " + loadErr + ")")}, nil
			}
			if d != "" {
				return detailContent{key: key, raw: d, renderable: true}, nil
			}
			if !cached {
				return detailContent{raw: stMuted.Render("(loading diff…)")}, cmd
			}
		}
		return detailContent{raw: stMuted.Render("(no changes in selected file)")}, nil
	}
	key := "range:" + m.detailView.diffBase + "..." + m.detailView.headRev
	head := m.detailView.headRev
	if !m.remote {
		head = ""
	}
	d, loadErr, cached, cmd := m.cachedRawDetail(key, func() (string, error) { return git.FileDiffRange(m.detailView.diffBase, head) })
	if loadErr != "" {
		return detailContent{raw: stMuted.Render("(diff unavailable: " + loadErr + ")")}, nil
	}
	if d != "" {
		return detailContent{key: key, raw: d, renderable: true}, nil
	}
	if !cached {
		return detailContent{raw: stMuted.Render("(loading diff…)")}, cmd
	}
	return detailContent{raw: stMuted.Render("(no changes in " + m.detailView.base + "..." + m.detailView.headRev + ")")}, nil
}

func (m *Model) loadCommitDetail(sha string) (detailContent, tea.Cmd) {
	if sha == "" {
		return detailContent{raw: stMuted.Render("no commit selected")}, nil
	}
	key := "commit:" + sha
	d, loadErr, cached, cmd := m.cachedRawDetail(key, func() (string, error) { return git.Show(sha) })
	if loadErr != "" {
		return detailContent{raw: stMuted.Render("(commit unavailable: " + loadErr + ")")}, nil
	}
	if d != "" {
		return detailContent{key: key, raw: d, renderable: true}, nil
	}
	if !cached {
		return detailContent{raw: stMuted.Render("(loading commit…)")}, cmd
	}
	return detailContent{raw: stMuted.Render("(commit " + sha + " not found in this repo)")}, nil
}
