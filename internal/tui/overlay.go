// The overlay seam: every modal popup implements one interface instead of
// registering a sentinel field in Model, a branch in Update, and a branch in
// View.
package tui

import tea "charm.land/bubbletea/v2"

// overlay is a modal popup layered over the current screen. Model.overlay
// holds the open popup; nil means none. While an overlay is open it owns the
// keyboard: Update routes every key to handleKey, and View stacks render on
// top of the screen. Async completion messages (asyncCompletion) bypass the
// overlay so background work keeps landing.
//
// Convention: overlays are stored as struct values, and handleKey receives a
// copy through its value receiver. A handler that changes overlay state must
// write it back with m.overlay = o — or close the popup with m.overlay = nil
// — before returning the model. Mutating state behind a shared pointer is
// forbidden: bubbletea models are values, and older copies must not observe
// later edits.
type overlay interface {
	handleKey(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd)
	render(m Model) string
}

// overlayMsgHandler is implemented by overlays that also need non-key
// messages; the editor overlay uses it for cursor blink. Overlays without it
// swallow non-key messages, keeping the keyboard-only modal contract.
type overlayMsgHandler interface {
	handleMsg(m Model, msg tea.Msg) (Model, tea.Cmd)
}

// cursorOverlay is implemented by overlays that host a text input. cursor
// returns the caret position relative to the popup's top-left cell, or nil
// when no input is focused; View adds the popup's centering offset so the
// real terminal cursor — and the IME composition window that follows it —
// lands on the caret.
type cursorOverlay interface {
	cursor(m Model) *tea.Cursor
}

// overlayHostsEditor reports whether the open overlay displays the shared
// localEditor textarea, so a terminal resize can refit it.
func (m Model) overlayHostsEditor() bool {
	switch o := m.overlay.(type) {
	case localEditOverlay:
		return true
	case reviewSubmitOverlay:
		return o.typing
	}
	return false
}
