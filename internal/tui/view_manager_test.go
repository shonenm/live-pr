package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/shonenm/live-pr/internal/config"
	gh "github.com/shonenm/live-pr/internal/github"
)

// The view manager edits a draft: Esc discards, Ctrl+S writes the config and
// reloads the tabs.
func TestViewManagerEditsReorderAndSave(t *testing.T) {
	global := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", global)
	m := testModel()
	m.screen, m.w, m.h = prListScreen, 120, 40
	m.root = t.TempDir()
	m.navigator = gh.NewNavigatorCache()
	m.navigatorPath = filepath.Join(t.TempDir(), "prs.json")

	press := func(model Model, keys ...string) Model {
		t.Helper()
		for _, k := range keys {
			var msg tea.KeyMsg
			switch k {
			case "enter":
				msg = tea.KeyMsg{Type: tea.KeyEnter}
			case "tab":
				msg = tea.KeyMsg{Type: tea.KeyTab}
			case "esc":
				msg = tea.KeyMsg{Type: tea.KeyEsc}
			case "ctrl+s":
				msg = tea.KeyMsg{Type: tea.KeyCtrlS}
			default:
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
			}
			u, _ := model.Update(msg)
			model = u.(Model)
		}
		return model
	}

	m = press(m, "V")
	if !m.viewManager || len(m.viewDraft) != len(config.DefaultViews()) {
		t.Fatalf("manager did not open: %v draft=%d", m.viewManager, len(m.viewDraft))
	}
	if popup := ansi.Strip(m.renderViewManagerPopup()); !strings.Contains(popup, "Assigned") || !strings.Contains(popup, "assignee:@me") {
		t.Fatalf("popup = %q", popup)
	}

	// The manager opens on the current tab; move to the top first.
	if m.viewCursor != int(m.prView) {
		t.Fatalf("manager opened on view %d, want %d", m.viewCursor, m.prView)
	}
	m.viewCursor = 0

	// Reorder: J swaps the first two tabs and follows the selection.
	m = press(m, "J")
	if m.viewDraft[0].Name != "Review requested" || m.viewDraft[1].Name != "Assigned" || m.viewCursor != 1 {
		t.Fatalf("reorder = %v cursor=%d", m.viewDraft, m.viewCursor)
	}

	// Rename the selected view through the two-field form.
	m = press(m, "e")
	if m.viewEditField != viewEditName {
		t.Fatal("edit form did not focus the name")
	}
	m.viewNameInput.SetValue("Mine")
	m = press(m, "tab")
	m.viewQueryInput.SetValue("author:@me")
	m = press(m, "enter")
	if m.viewEditField != viewEditNone || m.viewDraft[1].Name != "Mine" || m.viewDraft[1].Query != "author:@me" {
		t.Fatalf("edit not applied: %#v", m.viewDraft[1])
	}

	// A duplicate name is rejected instead of silently dropping a view.
	m = press(m, "e")
	m.viewNameInput.SetValue("review requested")
	m = press(m, "enter")
	if m.viewManagerError == "" || m.viewEditField == viewEditNone {
		t.Fatalf("duplicate name accepted: err=%q", m.viewManagerError)
	}
	m = press(m, "esc")

	// New view, then delete one.
	m = press(m, "n")
	m.viewNameInput.SetValue("Bugs")
	m = press(m, "tab")
	m.viewQueryInput.SetValue("label:bug")
	m = press(m, "enter")
	if last := m.viewDraft[len(m.viewDraft)-1]; last.Name != "Bugs" || last.Query != "label:bug" {
		t.Fatalf("new view = %#v", last)
	}
	before := len(m.viewDraft)
	m = press(m, "d")
	if len(m.viewDraft) != before-1 {
		t.Fatalf("delete kept %d views", len(m.viewDraft))
	}

	// Esc discards everything.
	discarded := press(m, "esc")
	if discarded.viewManager || len(discarded.views) != len(config.DefaultViews()) || discarded.views[0].Name != "Assigned" {
		t.Fatalf("esc did not discard: views=%v", discarded.views)
	}

	// Ctrl+S persists and applies. The tab that was selected before the edit
	// ("Review requested", index 1 of the defaults) keeps its selection even
	// though reordering moved it to the front.
	m.prView = 1
	saved := press(m, "ctrl+s")
	if saved.viewManager || saved.views[0].Name != "Review requested" || saved.views[1].Name != "Mine" {
		t.Fatalf("save did not apply: %v", saved.views)
	}
	if saved.viewName(saved.prView) != "Review requested" || saved.prView != 0 {
		t.Fatalf("selection did not follow the renamed order: %q (%d)", saved.viewName(saved.prView), saved.prView)
	}
	reloaded, err := config.Load(saved.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Views) != len(saved.views) || reloaded.Views[1].Name != "Mine" {
		t.Fatalf("config on disk = %#v", reloaded.Views)
	}
}
