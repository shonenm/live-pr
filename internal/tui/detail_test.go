package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/embeddedterm"
	"github.com/shonenm/live-pr/internal/event"
	"github.com/shonenm/live-pr/internal/git"
	gh "github.com/shonenm/live-pr/internal/github"
	"github.com/shonenm/live-pr/internal/store"
)

func TestLoadDetailCachesRawGitOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake executable")
	}
	dir := t.TempDir()
	counter := filepath.Join(dir, "calls")
	script := fmt.Sprintf("#!/bin/sh\ncount=0\n[ -f %q ] && read count < %q\ncount=$((count + 1))\nprintf '%%s' \"$count\" > %q\nprintf 'cached diff\\n'\n", counter, counter, counter)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	m := testModel()
	m.screen, m.detailView.base, m.detailView.headRev = detailScreen, "main", "HEAD"
	m.diffCommand, m.diffTerminal = "", nil

	// A cache miss dispatches the git work as a Cmd instead of blocking.
	first, cmd := m.loadDetail()
	if cmd == nil || first.renderable {
		t.Fatalf("cache miss did not dispatch: %#v cmd=%v", first, cmd)
	}
	if again, dup := m.loadDetail(); dup != nil || again.renderable {
		t.Fatalf("pending key dispatched twice: %#v cmd=%v", again, dup)
	}
	u, _ := m.Update(cmd().(rawDetailLoaded))
	m = u.(Model)
	second, hitCmd := m.loadDetail()
	calls, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if second.raw != "cached diff" || hitCmd != nil || string(calls) != "1" {
		t.Fatalf("detail = %#v cmd=%v calls=%q", second, hitCmd, calls)
	}
	m.detailView.resetCaches()
	_, missCmd := m.loadDetail()
	if missCmd == nil {
		t.Fatal("cache reset did not re-dispatch")
	}
	_ = missCmd()
	calls, _ = os.ReadFile(counter)
	if string(calls) != "2" {
		t.Fatalf("cache reset calls=%q", calls)
	}
}

func TestLoadDetailSurfacesGitErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX fake executable")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'fatal: bad revision' >&2\nexit 128\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	m := testModel()
	m.screen, m.detailView.base, m.detailView.headRev = detailScreen, "main", "HEAD"
	m.diffCommand, m.diffTerminal = "", nil

	_, cmd := m.loadDetail()
	if cmd == nil {
		t.Fatal("cache miss did not dispatch")
	}
	u, _ := m.Update(cmd().(rawDetailLoaded))
	m = u.(Model)
	detail, again := m.loadDetail()
	if again != nil {
		t.Fatal("failed load was not cached")
	}
	plain := ansi.Strip(detail.raw)
	if !strings.Contains(plain, "diff unavailable") || !strings.Contains(plain, "fatal: bad revision") {
		t.Fatalf("git failure rendered as %q, want a visible diff error", plain)
	}
	if detail.renderable {
		t.Fatal("error message must not be treated as a renderable diff")
	}
}

func TestStaticDiffUsesFileExplorerAndChecksFiles(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.diffCommand = ""
	m.diffTerminal = nil
	m.detailView.base, m.detailView.headRev = "main", "HEAD"
	m.detailView.files = []git.ChangedFile{
		{Status: "M", Path: "internal/tui/tui.go"},
		{Status: "A", Path: "internal/tui/explorer.go"},
		{Status: "D", Path: "internal/tui/legacy.go"},
		{Status: "R100", OldPath: "internal/tui/old.go", Path: "internal/tui/new.go"},
	}
	m.explorer.Width = 80

	content, selected := m.buildFileExplorer()
	plain := ansi.Strip(content)
	for _, want := range []string{
		"Files · 4 changed",
		"□ M internal/tui/tui.go",
		"□ A internal/tui/explorer.go",
		"□ D internal/tui/legacy.go",
		"□ R100 internal/tui/old.go → internal/tui/new.go",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("explorer missing %q: %q", want, plain)
		}
	}
	if selected != 1 {
		t.Fatalf("selected explorer row = %d, want 1", selected)
	}

	m.toggleFileCheck()
	content, _ = m.buildFileExplorer()
	plain = ansi.Strip(content)
	if !strings.Contains(plain, "✓ M internal/tui/tui.go") {
		t.Fatalf("checked file missing: %q", plain)
	}
}

func TestStaticDiffExplorerAndDiffNavigation(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.diffCommand = ""
	m.ready = true
	m.detailView.files = []git.ChangedFile{
		{Status: "M", Path: "internal/tui/tui.go"},
		{Status: "A", Path: "internal/tui/explorer.go"},
	}
	m.detailView.focus = focusExplorer
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = u.(Model)
	if m.detailView.fileCursor != 1 {
		t.Fatalf("file cursor = %d, want 1", m.detailView.fileCursor)
	}

	m.detailView.fileCursor = 0
	m.detail.Width, m.detail.Height = 40, 3
	m.detail.SetContent(strings.Repeat("line\n", 20))
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = u.(Model)
	if m.detail.YOffset == 0 {
		t.Fatal("ctrl+d did not scroll the diff")
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = u.(Model)
	if m.detailView.fileCursor != 1 {
		t.Fatalf("G file cursor = %d, want 1", m.detailView.fileCursor)
	}
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = u.(Model)
	if m.detailView.fileCursor != 0 {
		t.Fatalf("gg file cursor = %d, want 0", m.detailView.fileCursor)
	}

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = u.(Model)
	if !m.detailView.fileChecked(m.detailView.files[m.detailView.fileCursor]) {
		t.Fatal("c did not check the selected file from Diff")
	}
}

func TestRemotePRHeaderAndExplorerShowMergeReadiness(t *testing.T) {
	m := testModel()
	m.w = 180
	m.remote = true
	m.cache.PR = &gh.PR{Number: 7, State: "OPEN"}
	m.detailView.mergeReadiness = git.MergeReadiness{Behind: 3, ConflictFiles: []string{"conflict.go"}}
	m.detailView.files = []git.ChangedFile{{Status: "M", Path: "conflict.go"}, {Status: "A", Path: "clean.go"}}
	m.explorer.Width = 80
	header := ansi.Strip(m.renderHeader())
	if !strings.Contains(header, "3 behind") || !strings.Contains(header, "1 conflict files") {
		t.Fatalf("merge readiness header = %q", header)
	}
	explorer, _ := m.buildFileExplorer()
	plain := ansi.Strip(explorer)
	if !strings.Contains(plain, "⚠ 1 conflicts") || !strings.Contains(plain, "⚠ M conflict.go") || strings.Contains(plain, "⚠ A clean.go") {
		t.Fatalf("merge readiness explorer = %q", plain)
	}
}

func TestConflictAndCheckViewsUseLeftPane(t *testing.T) {
	m := testModel()
	m.screen = detailScreen
	m.detailView.mergeReadiness = git.MergeReadiness{Behind: 3, ConflictFiles: []string{"conflict.go", "nested/other.go"}}
	m.cache.PR = &gh.PR{Checks: []gh.PRCheck{
		{Name: "unit", WorkflowName: "CI", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Context: "lint", Status: "IN_PROGRESS"},
		{Name: "deploy", State: "FAILURE"},
	}}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m = updated.(Model)
	plain := ansi.Strip(m.View())
	if m.detailView.active != conflictsTab || !strings.Contains(plain, "Conflicts · 2") || !strings.Contains(plain, "⚠ conflict.go") {
		t.Fatalf("conflict view = active:%v view:%q", m.detailView.active, plain)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = updated.(Model)
	plain = ansi.Strip(m.View())
	for _, want := range []string{"Checks · 3", "out of date · 3 commits behind base", "✓ unit · CI · success", "◐ lint · in progress", "✗ deploy · failure"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("check view missing %q: %q", want, plain)
		}
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.detailView.active != conversationTab {
		t.Fatalf("Esc active = %v, want Conversation", m.detailView.active)
	}
}

func TestGitHubConflictingKeepsConflictView(t *testing.T) {
	m := testModel()
	m.screen, m.remote = detailScreen, true
	m.cache.PR = &gh.PR{Number: 8, Mergeable: "CONFLICTING"}
	m.detailView.mergeReadiness, m.detailView.mergeReadinessErr = applyGitHubConflictFallback(git.MergeReadiness{}, nil, *m.cache.PR)
	out, _ := m.buildConflicts()
	if !strings.Contains(ansi.Strip(out), "GitHub reports conflicts") {
		t.Fatalf("conflicting PR hid conflicts: %q", ansi.Strip(out))
	}
	dirty := gh.PR{Number: 9, MergeStateStatus: "DIRTY"}
	readiness, err := applyGitHubConflictFallback(git.MergeReadiness{}, nil, dirty)
	if err != nil || len(readiness.ConflictFiles) != 0 {
		t.Fatalf("DIRTY status should not invent conflicts: %#v err=%v", readiness, err)
	}
}

func TestCommitPickerShowsCommitSpecificCI(t *testing.T) {
	m := testModel()
	m.detailView.commits = []git.Commit{{SHA: "abc12341", Subject: "first"}, {SHA: "abc12342", Subject: "second"}}
	m.cache.PR = &gh.PR{Commits: []gh.PRCommit{
		{OID: "abc1234100000000000000000000000000000000", CheckRollupState: "SUCCESS"},
		{OID: "abc1234200000000000000000000000000000000", CheckRollupState: "FAILURE"},
	}}
	out, _ := m.buildCommits()
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "✓ abc12341 first") || !strings.Contains(plain, "✗ abc12342 second") {
		t.Fatalf("commit CI statuses missing or collided: %q", plain)
	}
	m.remote = true
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = u.(Model)
	if m.detailView.active != commitsTab || !strings.Contains(ansi.Strip(m.View()), "Commits · 2") {
		t.Fatalf("remote c did not open commit list: active=%v view=%q", m.detailView.active, ansi.Strip(m.View()))
	}
}

func TestCommitPickerSelectsCommitAndEscRestoresBranchReview(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = u.(Model)
	if m.detailView.active != commitsTab || !strings.Contains(m.View(), "feat: x") {
		t.Fatalf("c should replace Conversation with the commit picker")
	}

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if m.detailView.reviewSHA != "abc1234" || !strings.Contains(ansi.Strip(m.renderHeader()), "commit abc1234") {
		t.Fatalf("Enter did not select commit review: sha=%q", m.detailView.reviewSHA)
	}

	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = u.(Model)
	if m.detailView.active != conversationTab || m.detailView.reviewSHA != "" || m.detailView.cursors[conversationTab] != 0 {
		t.Fatalf("Esc should restore branch review and the Conversation cursor")
	}
}

func TestCommitPickerCancelKeepsBranchTerminal(t *testing.T) {
	m := testModel()
	m.diffTerminal = embeddedterm.New("cat", t.TempDir(), nil)
	branchTerminal := m.diffTerminal
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = u.(Model)
	defer m.close()
	if m.detailView.active != conversationTab || m.diffTerminal != branchTerminal || m.detailView.reviewSHA != "" {
		t.Fatal("canceling an unselected picker restarted branch review")
	}
}

func TestReloadLocalConversationReadsExternalCLIChanges(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	st := store.ForBranch(root, "feature")
	if err := st.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.Conclusion(), []byte("# Final title\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created, err := event.Create(st.Timeline(), event.Event{TS: "2026-08-11T10:00", Kind: event.Decision, Title: "external", Author: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	m := testModel()
	m.root, m.currentBranch, m.timelinePath = root, "feature", st.Timeline()
	m.detailView.events = nil
	m.reloadLocalConversation()
	if len(m.detailView.events) != 1 || m.detailView.events[0].ID != created.ID || m.detailView.title != "Final title" {
		t.Fatalf("reloaded local state = title %q events %+v", m.detailView.title, m.detailView.events)
	}
	items := m.conversationItems()
	if len(items) != 2 || items[0].summary == nil || items[1].event == nil {
		t.Fatalf("local summary must lead conversation: %#v", items)
	}
}

func TestTranslateDiffMouseUsesContentBounds(t *testing.T) {
	headerHeight := logoHeight + 1 // header rows plus the review pane's top border
	msg := tea.MouseMsg{X: 42, Y: headerHeight, Action: tea.MouseActionPress}
	local, ok := translateDiffMouse(msg, 40, 80, 20, headerHeight)
	if !ok || local.X != 0 || local.Y != 0 {
		t.Fatalf("translated = %+v, ok=%v", local, ok)
	}
	for _, outside := range []tea.MouseMsg{
		{X: 41, Y: headerHeight},
		{X: 122, Y: headerHeight},
		{X: 42, Y: headerHeight - 1},
		{X: 42, Y: headerHeight + 20},
	} {
		if _, ok := translateDiffMouse(outside, 40, 80, 20, headerHeight); ok {
			t.Fatalf("outside event accepted: %+v", outside)
		}
	}
}

func TestConfiguredDiffDisplayIsAsyncCachedAndRejectsStaleResults(t *testing.T) {
	m := testModel()
	m.diffDisplay = "sed 's/foo/bar/g'"
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	detail := detailContent{key: "commit:abc", raw: "foo", renderable: true}
	cmd := m.syncDetail(detail)
	if cmd == nil || !strings.Contains(m.detail.View(), "foo") {
		t.Fatal("raw diff should display while configured command runs")
	}
	if duplicate := m.syncDetail(detail); duplicate != nil {
		t.Fatal("same diff command should be single-flight")
	}
	msg := cmd().(diffRendered)
	u, _ = m.Update(msg)
	m = u.(Model)
	if !strings.Contains(m.detail.View(), "bar") {
		t.Fatalf("rendered diff not applied: %q", m.detail.View())
	}
	if cached := m.syncDetail(detail); cached != nil {
		t.Fatal("rendered diff should be cached")
	}

	m.detail.SetContent("current")
	m.detailView.diffKey = "current-key"
	u, _ = m.Update(diffRendered{key: "stale-key", output: "stale", raw: "raw", err: errors.New("diff display stale failure")})
	m = u.(Model)
	if strings.Contains(m.detail.View(), "stale") || strings.Contains(m.status, "stale failure") {
		t.Fatal("late result affected the current selection")
	}
}

func TestDefaultDiffDisplayUsesRawOutput(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	if cmd := m.syncDetail(detailContent{key: "commit:abc", raw: "raw diff", renderable: true}); cmd != nil || !strings.Contains(m.detail.View(), "raw diff") {
		t.Fatal("empty config must keep raw Git output without a command")
	}
}

func TestConfiguredDiffFailureKeepsRawOutput(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	m.diffDisplay = "exit 2"
	detail := detailContent{key: "commit:abc", raw: "raw diff", renderable: true}
	cmd := m.syncDetail(detail)
	u, _ = m.Update(cmd())
	m = u.(Model)
	if !strings.Contains(m.detail.View(), "raw diff") || !strings.Contains(m.status, "diff display") {
		t.Fatalf("failure did not fall back to raw diff: detail=%q status=%q", m.detail.View(), m.status)
	}
	if retry := m.syncDetail(detail); retry != nil || !strings.Contains(m.detail.View(), "raw diff") || m.status != "" {
		t.Fatalf("failed result should be cached and its error cleared after navigation")
	}
}

func TestSyncDetailKeepsScrollWhenContentUnchanged(t *testing.T) {
	m := testModel()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	long := strings.Repeat("line\n", 100)
	detail := detailContent{key: "commit:abc", raw: long, renderable: true}
	m.syncDetail(detail)
	m.detail.SetYOffset(7)
	m.syncDetail(detail)
	if m.detail.YOffset != 7 {
		t.Fatalf("re-sync of unchanged content moved scroll to %d, want 7", m.detail.YOffset)
	}
	other := strings.Repeat("other\n", 100)
	m.syncDetail(detailContent{key: "commit:def", raw: other, renderable: true})
	if m.detail.YOffset != 0 || !strings.Contains(m.detail.View(), "other") {
		t.Fatalf("new content should reset to top: offset=%d view=%q", m.detail.YOffset, m.detail.View())
	}
}

func TestSyncDetailKeepsScrollOnRenderedDiffCacheHits(t *testing.T) {
	m := testModel()
	m.diffDisplay = "cat"
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	detail := detailContent{key: "commit:abc", raw: strings.Repeat("line\n", 100), renderable: true}
	cmd := m.syncDetail(detail)
	if cmd == nil {
		t.Fatal("first sync should dispatch the configured diff command")
	}
	u, _ = m.Update(cmd())
	m = u.(Model)
	m.detail.SetYOffset(7)
	if extra := m.syncDetail(detail); extra != nil || m.detail.YOffset != 7 {
		t.Fatalf("cached rendered diff re-sync moved scroll to %d, want 7", m.detail.YOffset)
	}
}

func TestCommitSelectionStartsEmbeddedCommitCommandAndFocusesReview(t *testing.T) {
	m := testModel()
	m.root = t.TempDir()
	m.diffCommitCommand = "cat"
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = u.(Model)
	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	defer m.close()
	if cmd == nil || m.detailView.reviewSHA != "abc1234" || m.detailView.focus != focusReview || m.diffTerminal == nil {
		t.Fatalf("commit selection did not start/focus embedded review: sha=%q focus=%v terminal=%v", m.detailView.reviewSHA, m.detailView.focus, m.diffTerminal != nil)
	}
}

func TestConflictsViewShowsBehindCount(t *testing.T) {
	m := testModel()
	m.detailView.mergeReadiness = git.MergeReadiness{Behind: 3}
	// No conflicting files, but behind base: the count must still show.
	out, _ := m.buildConflicts()
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "3 commits behind base") || !strings.Contains(plain, "no conflicting files") {
		t.Fatalf("conflicts view = %q", plain)
	}
	// With conflicts, the behind header still leads.
	m.detailView.mergeReadiness = git.MergeReadiness{Behind: 1, ConflictFiles: []string{"a.go"}}
	out, _ = m.buildConflicts()
	plain = ansi.Strip(out)
	if !strings.Contains(plain, "1 commit behind base") || !strings.Contains(plain, "a.go") {
		t.Fatalf("conflicts view with files = %q", plain)
	}
}

// A new commit changes the review range and the head revision, but GitHub only
// clears "viewed" for files whose own diff changed. Marks are therefore keyed
// by path and validated against the file's diff fingerprint.
func TestReviewedMarksSurviveCommitsThatTouchOtherFiles(t *testing.T) {
	m := testModel()
	m.detailView.files = []git.ChangedFile{
		{Status: "M", Path: "kept.go", Fingerprint: "aaa:bbb"},
		{Status: "M", Path: "edited.go", Fingerprint: "ccc:ddd"},
	}
	m.detailView.fileCursor = 0
	m.toggleFileCheck()
	m.detailView.fileCursor = 1
	m.toggleFileCheck()
	for _, file := range m.detailView.files {
		if !m.detailView.fileChecked(file) {
			t.Fatalf("%s was not checked", file.Path)
		}
	}

	// A commit lands: the range moves and edited.go's diff changes, while
	// kept.go's diff is untouched.
	m.detailView.diffBase, m.detailView.headRev = "newbase", "newhead"
	m.detailView.files = []git.ChangedFile{
		{Status: "M", Path: "kept.go", Fingerprint: "aaa:bbb"},
		{Status: "M", Path: "edited.go", Fingerprint: "ccc:eee"},
	}
	if !m.detailView.fileChecked(m.detailView.files[0]) {
		t.Error("kept.go lost its reviewed mark even though its diff is unchanged")
	}
	if m.detailView.fileChecked(m.detailView.files[1]) {
		t.Error("edited.go stayed reviewed even though its diff changed")
	}
}

// Stacked PRs share paths and often identical per-file diffs, so marks must be
// scoped per PR — switching must never show another PR's progress.
func TestReviewedMarksAreScopedPerPR(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.detailView.files = []git.ChangedFile{{Status: "M", Path: "shared.go", Fingerprint: "aaa:bbb"}}

	// PR #1: check the file; the mark is persisted to #1's file.
	m.loadReviewedMarks(1, "feature")
	m.toggleFileCheck()
	if !m.detailView.fileChecked(m.detailView.files[0]) {
		t.Fatal("mark not set for PR #1")
	}

	// Switching to stacked PR #2 with the same path+fingerprint: clean slate.
	m.loadReviewedMarks(2, "feature")
	if m.detailView.fileChecked(m.detailView.files[0]) {
		t.Fatal("PR #1's mark leaked into PR #2")
	}

	// Back to PR #1: the persisted mark is restored.
	m.loadReviewedMarks(1, "feature")
	if !m.detailView.fileChecked(m.detailView.files[0]) {
		t.Fatal("PR #1's mark was lost after switching away")
	}
}

func TestReviewedMarksPersistAcrossSessions(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testModel()
	m.root = t.TempDir()
	m.detailView.files = []git.ChangedFile{{Status: "M", Path: "a.go", Fingerprint: "f1"}}
	m.loadReviewedMarks(7, "feature")
	m.toggleFileCheck()

	// A fresh model (new session) sees the same marks from disk.
	fresh := testModel()
	fresh.root = m.root
	fresh.detailView.files = m.detailView.files
	fresh.loadReviewedMarks(7, "feature")
	if !fresh.detailView.fileChecked(fresh.detailView.files[0]) {
		t.Fatal("mark did not survive a session restart")
	}
}

func TestBaseResolvedAppliesOnlyCurrentGeneration(t *testing.T) {
	m := testModel()
	m.targetGeneration = 3
	m.detailView.base, m.detailView.diffBase, m.detailView.headRev, m.detailView.reviewRange = "main", "old-base", "HEAD", "old-base"

	// Stale generation: dropped.
	u, _ := m.Update(baseResolved{generation: 2, base: "main", diffBase: "new-base", headRev: "HEAD", reviewRange: "new-base"})
	if u.(Model).detailView.diffBase != "old-base" {
		t.Fatalf("stale baseResolved applied: %q", u.(Model).detailView.diffBase)
	}

	// Unchanged range: no-op.
	u, _ = m.Update(baseResolved{generation: 3, base: "main", diffBase: "old-base", headRev: "HEAD", reviewRange: "old-base"})
	if u.(Model).detailView.fileCursor != 0 && u.(Model).detailView.diffBase != "old-base" {
		t.Fatal("unchanged range should be a no-op")
	}

	// Changed range: applied with the gathered scans.
	u, _ = m.Update(baseResolved{
		generation: 3, base: "main", diffBase: "new-base", headRev: "HEAD", reviewRange: "new-base",
		commits: []git.Commit{{SHA: "abc"}}, files: []git.ChangedFile{{Status: "M", Path: "x.go"}},
	})
	m = u.(Model)
	if m.detailView.diffBase != "new-base" || m.detailView.reviewRange != "new-base" || len(m.detailView.commits) != 1 || len(m.detailView.files) != 1 {
		t.Fatalf("baseResolved not applied: diffBase=%q commits=%d files=%d", m.detailView.diffBase, len(m.detailView.commits), len(m.detailView.files))
	}
}

func TestRefreshAppliesFreshReadinessOnUnchangedRange(t *testing.T) {
	m := testModel()
	m.targetGeneration = 3
	m.detailView.base, m.detailView.diffBase, m.detailView.headRev, m.detailView.reviewRange = "main", "origin/main", "HEAD", "origin/main"
	m.detailView.mergeReadiness = git.MergeReadiness{Behind: 0}
	m.detailView.commits = []git.Commit{{SHA: "old"}}

	// The range string is unchanged, but the base ref moved underneath it:
	// behind count, conflicts, and the scans must still refresh.
	u, _ := m.Update(baseResolved{
		generation: 3, base: "main", diffBase: "origin/main", headRev: "HEAD", reviewRange: "origin/main",
		commits:     []git.Commit{{SHA: "new1"}, {SHA: "new2"}},
		files:       []git.ChangedFile{{Status: "M", Path: "a.go"}},
		readiness:   git.MergeReadiness{Behind: 4, ConflictFiles: []string{"a.go"}},
		readinessOK: true,
	})
	m = u.(Model)
	if m.detailView.mergeReadiness.Behind != 4 || len(m.detailView.mergeReadiness.ConflictFiles) != 1 {
		t.Fatalf("stale readiness kept: %#v", m.detailView.mergeReadiness)
	}
	if len(m.detailView.commits) != 2 || len(m.detailView.files) != 1 {
		t.Fatalf("stale scans kept: commits=%d files=%d", len(m.detailView.commits), len(m.detailView.files))
	}
	if m.diffTerminal != nil {
		t.Fatal("unchanged range must not restart the review terminal")
	}
}

// Switching the detail target to a different PR prunes richBodies: entries are
// keyed by full body text, so another target's bodies are never looked up
// again and previously accumulated for the whole session. Reopening the same
// PR keeps them, matching the resetCaches comment that preserves rendered
// mermaid across same-target reloads.
func TestTargetSwitchPrunesRichBodies(t *testing.T) {
	m := testModel()
	m.screen, m.remote = detailScreen, true
	m.cache = gh.NewCache("feature")
	m.cache.PR = &gh.PR{Number: 1, HeadRefName: "feature"}
	m.detailView.richBodies = map[string]string{"body": "rendered"}
	m.detailView.lastRichContentKey = [32]byte{1}

	_ = m.openRemote(gh.PR{Number: 1, BaseRefName: "main", HeadRefName: "feature"})
	if len(m.detailView.richBodies) != 1 {
		t.Fatalf("same-PR reopen pruned richBodies: %#v", m.detailView.richBodies)
	}

	m.detailView.lastRichContentKey = [32]byte{1}
	_ = m.openRemote(gh.PR{Number: 2, BaseRefName: "main", HeadRefName: "other"})
	if len(m.detailView.richBodies) != 0 {
		t.Fatalf("PR switch kept richBodies: %#v", m.detailView.richBodies)
	}
	if m.detailView.lastRichContentKey != ([32]byte{}) {
		t.Fatal("PR switch kept the rich-content dispatch key")
	}
}

func TestApplyLocalFromRemotePrunesRichBodies(t *testing.T) {
	st := store.ForBranch(t.TempDir(), "feature")
	m := testModel()
	m.screen, m.remote = detailScreen, true
	m.detailView.richBodies = map[string]string{"body": "rendered"}
	m.applyLocal(st, localData{cache: gh.NewCache("feature")})
	if len(m.detailView.richBodies) != 0 {
		t.Fatal("remote-to-local switch kept richBodies")
	}

	// A local reload of the same branch keeps the rendered bodies.
	m.detailView.richBodies = map[string]string{"body": "rendered"}
	m.applyLocal(st, localData{cache: gh.NewCache("feature")})
	if len(m.detailView.richBodies) != 1 {
		t.Fatal("local reload pruned richBodies")
	}
}

// The commits and checks tabs rebuilt every styled row on each sync; with
// 1,000 commits that costs several milliseconds per keystroke. The render is
// cached by (cursor, width, content fingerprint) like the conversation tab.
func TestBuildCommitsCachesRenderUntilInputsChange(t *testing.T) {
	m := testModel()
	m.detailView.commits = []git.Commit{{SHA: "abc12341", Subject: "first"}, {SHA: "abc12342", Subject: "second"}}
	m.cache.PR = &gh.PR{Commits: []gh.PRCommit{
		{OID: "abc1234100000000000000000000000000000000", CheckRollupState: "SUCCESS"},
		{OID: "abc1234200000000000000000000000000000000", CheckRollupState: "PENDING"},
	}}
	first, _ := m.buildCommits()
	m.detailView.commitsRender = "sentinel"
	if out, _ := m.buildCommits(); out != "sentinel" {
		t.Fatal("unchanged inputs rebuilt the commit rows")
	}
	m.cache.PR.Commits[1].CheckRollupState = "FAILURE"
	out, _ := m.buildCommits()
	if out == "sentinel" || out == first {
		t.Fatalf("rollup change did not recompute the rows: %q", ansi.Strip(out))
	}
	if !strings.Contains(ansi.Strip(out), "✗ abc12342") {
		t.Fatalf("recomputed rows missing new CI state: %q", ansi.Strip(out))
	}
	m.detailView.commitsRender = "sentinel"
	m.detailView.cursors[commitsTab] = 1
	if out, _ := m.buildCommits(); out == "sentinel" {
		t.Fatal("cursor move did not recompute the rows")
	}
}

func TestBuildChecksCachesRenderUntilInputsChange(t *testing.T) {
	m := testModel()
	m.cache.PR = &gh.PR{Checks: []gh.PRCheck{{Name: "build", Conclusion: "SUCCESS"}, {Name: "lint", Status: "IN_PROGRESS"}}}
	first, _ := m.buildChecks()
	m.detailView.checksRender = "sentinel"
	if out, _ := m.buildChecks(); out != "sentinel" {
		t.Fatal("unchanged inputs rebuilt the check rows")
	}
	m.cache.PR.Checks[1].Status = ""
	m.cache.PR.Checks[1].Conclusion = "FAILURE"
	out, _ := m.buildChecks()
	if out == "sentinel" || out == first {
		t.Fatal("check state change did not recompute the rows")
	}
	if !strings.Contains(ansi.Strip(out), "✗ lint") {
		t.Fatalf("recomputed rows missing new state: %q", ansi.Strip(out))
	}
}
