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

	// vm reads the open manager overlay; edit mutates a copy and stores it
	// back, matching the value-overlay convention.
	vm := func(m Model) viewManagerOverlay {
		t.Helper()
		o, ok := m.overlay.(viewManagerOverlay)
		if !ok {
			t.Fatalf("view manager not open: %#v", m.overlay)
		}
		return o
	}
	edit := func(m Model, mutate func(*viewManagerOverlay)) Model {
		t.Helper()
		o := vm(m)
		mutate(&o)
		m.overlay = o
		return m
	}

	m = press(m, "V")
	if o := vm(m); len(o.draft) != len(config.DefaultViews()) {
		t.Fatalf("manager did not open: draft=%d", len(o.draft))
	}
	if popup := ansi.Strip(vm(m).render(m)); !strings.Contains(popup, "Assigned") || !strings.Contains(popup, "assignee:@me") {
		t.Fatalf("popup = %q", popup)
	}

	// The manager opens on the current tab; move to the top first.
	if vm(m).cursor != int(m.prList.view) {
		t.Fatalf("manager opened on view %d, want %d", vm(m).cursor, m.prList.view)
	}
	m = edit(m, func(o *viewManagerOverlay) { o.cursor = 0 })

	// Reorder: J swaps the first two tabs and follows the selection.
	m = press(m, "J")
	if o := vm(m); o.draft[0].Name != "Review requested" || o.draft[1].Name != "Assigned" || o.cursor != 1 {
		t.Fatalf("reorder = %v cursor=%d", o.draft, o.cursor)
	}

	// Rename the selected view through the two-field form.
	m = press(m, "e")
	if vm(m).editField != viewEditName {
		t.Fatal("edit form did not focus the name")
	}
	m = edit(m, func(o *viewManagerOverlay) { o.nameInput.SetValue("Mine") })
	m = press(m, "tab")
	m = edit(m, func(o *viewManagerOverlay) { o.queryInput.SetValue("author:@me") })
	m = press(m, "enter")
	if o := vm(m); o.editField != viewEditNone || o.draft[1].Name != "Mine" || o.draft[1].Query != "author:@me" {
		t.Fatalf("edit not applied: %#v", o.draft[1])
	}

	// A duplicate name is rejected instead of silently dropping a view.
	m = press(m, "e")
	m = edit(m, func(o *viewManagerOverlay) { o.nameInput.SetValue("review requested") })
	m = press(m, "enter")
	if o := vm(m); o.errText == "" || o.editField == viewEditNone {
		t.Fatalf("duplicate name accepted: err=%q", o.errText)
	}
	m = press(m, "esc")

	// New view, then delete one.
	m = press(m, "n")
	m = edit(m, func(o *viewManagerOverlay) { o.nameInput.SetValue("Bugs") })
	m = press(m, "tab")
	m = edit(m, func(o *viewManagerOverlay) { o.queryInput.SetValue("label:bug") })
	m = press(m, "enter")
	if last := vm(m).draft[len(vm(m).draft)-1]; last.Name != "Bugs" || last.Query != "label:bug" {
		t.Fatalf("new view = %#v", last)
	}
	before := len(vm(m).draft)
	m = press(m, "d")
	if len(vm(m).draft) != before-1 {
		t.Fatalf("delete kept %d views", len(vm(m).draft))
	}

	// Esc discards everything.
	discarded := press(m, "esc")
	if discarded.overlay != nil || len(discarded.views) != len(config.DefaultViews()) || discarded.views[0].Name != "Assigned" {
		t.Fatalf("esc did not discard: views=%v", discarded.views)
	}

	// Ctrl+S persists and applies. The tab that was selected before the edit
	// ("Review requested", index 1 of the defaults) keeps its selection even
	// though reordering moved it to the front.
	m.prList.view = 1
	saved := press(m, "ctrl+s")
	if saved.overlay != nil || saved.views[0].Name != "Review requested" || saved.views[1].Name != "Mine" {
		t.Fatalf("save did not apply: %v", saved.views)
	}
	if saved.viewName(saved.prList.view) != "Review requested" || saved.prList.view != 0 {
		t.Fatalf("selection did not follow the renamed order: %q (%d)", saved.viewName(saved.prList.view), saved.prList.view)
	}
	reloaded, err := config.Load(saved.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Views) != len(saved.views) || reloaded.Views[1].Name != "Mine" {
		t.Fatalf("config on disk = %#v", reloaded.Views)
	}
}
