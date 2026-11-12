package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	gh "github.com/shonenm/live-pr/v2/internal/github"
)

// sized delivers a window size, which lays the model out and runs the first
// sync, so the key states describe the screen under test.
func sized(t *testing.T, m Model) Model {
	t.Helper()
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return u.(Model)
}

// TestHelpListsKeysTheDetailHandlerAccepts pins the help to the key handler:
// every action that answers on the detail screen must also be listed.
func TestHelpListsKeysTheDetailHandlerAccepts(t *testing.T) {
	m := testModel()
	m.screen, m.remote, m.currentBranch = detailScreen, true, "main"
	m.cache.PR = &gh.PR{Number: 14, State: "OPEN", URL: "https://example/pr/14", HeadRefName: "feature", HeadRefOID: "abc"}
	m.detailView.active = commitsTab
	m = sized(t, m)

	help := m.contextualKeys()
	for name, binding := range map[string]key.Binding{
		"Merge": help.Merge, "Checkout": help.Checkout, "EditLocal": help.EditLocal, "DeleteLocal": help.DeleteLocal,
		"Select": help.Select, "Back": help.Back, "FocusRight": help.FocusRight,
	} {
		if !binding.Enabled() {
			t.Errorf("detail help omits %s although the handler acts on it", name)
		}
	}
	if got := help.Checkout.Help().Key; got != "C" {
		t.Errorf("detail checkout help key = %q; c opens the commits tab there", got)
	}
	u, _ := m.Update(keyPress("m"))
	if got := u.(Model); got.pendingPRAction() != mergePR {
		t.Fatalf("m = pending:%v", got.pendingPRAction())
	}
	u, _ = m.Update(keyPress("C"))
	if got := u.(Model); got.pendingPRAction() != checkoutPR {
		t.Fatalf("C = pending:%v", got.pendingPRAction())
	}

	// The comment keys go to the reviewer once it has focus.
	m.detailView.focus = focusReview
	m.sync()
	help = m.contextualKeys()
	if help.AddComment.Enabled() || help.EditLocal.Enabled() || help.Review.Enabled() {
		t.Error("review focus still lists the conversation's comment keys")
	}
}

func TestHelpListsBrowseOnThePRList(t *testing.T) {
	m := testModel()
	m.screen = prListScreen
	m.prList.open = []gh.PR{{Number: 7, State: "OPEN", URL: "https://example/pr/7", HeadRefOID: "abc", HeadRefName: "other"}}
	m = sized(t, m)
	if !m.contextualKeys().Browse.Enabled() {
		t.Fatal("PR list help omits o although the handler opens the selected PR")
	}
}

// TestConfirmFooterNamesTheKeysThatConfirm guards against advertising Enter
// for the checkout and close popups, which answer to y alone.
func TestConfirmFooterNamesTheKeysThatConfirm(t *testing.T) {
	m := testModel()
	m.screen, m.currentBranch = prListScreen, "main"
	m.prList.open = []gh.PR{{Number: 14, State: "OPEN", HeadRefName: "feature", HeadRefOID: "abc"}}
	m = sized(t, m)

	for _, tc := range []struct {
		open, want string
		action     prAction
	}{
		{"m", "y/↵", mergePR},
		{"c", "y", checkoutPR},
		{"x", "y", closePR},
	} {
		u, _ := m.Update(keyPress(tc.open))
		popup := u.(Model)
		if popup.pendingPRAction() != tc.action {
			t.Fatalf("%s = pending:%v", tc.open, popup.pendingPRAction())
		}
		if got := popup.contextualKeys().Select.Help().Key; got != tc.want {
			t.Errorf("%s popup: confirm key in help = %q, want %q", tc.open, got, tc.want)
		}
		if got := ansi.Strip(popup.compactFooterContent()); !strings.HasPrefix(got, tc.want+" confirm") {
			t.Errorf("%s popup: compact footer = %q", tc.open, got)
		}
		u, cmd := popup.Update(keyPress("enter"))
		if confirmed := u.(Model).prActionRunning() == tc.action && cmd != nil; confirmed != strings.Contains(tc.want, "↵") {
			t.Errorf("%s popup: enter confirmed = %v, footer says %q", tc.open, confirmed, tc.want)
		}
	}
}
