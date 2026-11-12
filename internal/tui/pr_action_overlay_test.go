package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	gh "github.com/shonenm/live-pr/internal/github"
)

// A sync while an action ran used to disable the action keys, and a failed
// action never synced again, so m stayed dead until the selection moved.
func TestPRActionKeysWorkAgainAfterAFailedAction(t *testing.T) {
	m := testModel()
	m.screen, m.currentBranch = prListScreen, "main"
	m.prList.open = []gh.PR{{Number: 14, State: "OPEN", HeadRefName: "feature", HeadRefOID: "abc"}}
	for _, msg := range []tea.Msg{
		tea.WindowSizeMsg{Width: 120, Height: 40},
		keyPress("m"),
		keyPress("y"),
		tea.WindowSizeMsg{Width: 121, Height: 40}, // any sync while the merge runs
		prActionDone{action: mergePR, number: 14, err: errors.New("blocked")},
	} {
		u, _ := m.Update(msg)
		m = u.(Model)
	}
	if strings.Contains(ansi.Strip(m.viewContent()), "Merge PR #14") {
		t.Fatal("the popup outlived the failed merge")
	}
	u, _ := m.Update(keyPress("m"))
	m = u.(Model)
	if !strings.Contains(ansi.Strip(m.viewContent()), "Merge PR #14") {
		t.Fatal("m did not reopen the merge popup after a failed merge")
	}
}
