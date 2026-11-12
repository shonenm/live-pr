//go:build !windows

package embeddedterm

import (
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"
	teav1 "github.com/charmbracelet/bubbletea"
)

// Every key typed into the review pane crosses this seam, and the mapping
// leans on both Bubble Tea versions keeping their key constants where they
// are: v1 control keys equal their control characters and v1 function keys
// count downward.
func TestToEmulatorMsgConvertsKeys(t *testing.T) {
	const (
		shift = tea.ModShift
		ctrl  = tea.ModCtrl
		alt   = tea.ModAlt
	)
	for name, test := range map[string]struct {
		in   tea.KeyPressMsg
		want teav1.KeyMsg
	}{
		"up":              {tea.KeyPressMsg{Code: tea.KeyUp}, teav1.KeyMsg{Type: teav1.KeyUp}},
		"shift+up":        {tea.KeyPressMsg{Code: tea.KeyUp, Mod: shift}, teav1.KeyMsg{Type: teav1.KeyShiftUp}},
		"ctrl+up":         {tea.KeyPressMsg{Code: tea.KeyUp, Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlUp}},
		"ctrl+shift+up":   {tea.KeyPressMsg{Code: tea.KeyUp, Mod: ctrl | shift}, teav1.KeyMsg{Type: teav1.KeyCtrlShiftUp}},
		"alt+up":          {tea.KeyPressMsg{Code: tea.KeyUp, Mod: alt}, teav1.KeyMsg{Type: teav1.KeyUp, Alt: true}},
		"shift+down":      {tea.KeyPressMsg{Code: tea.KeyDown, Mod: shift}, teav1.KeyMsg{Type: teav1.KeyShiftDown}},
		"ctrl+right":      {tea.KeyPressMsg{Code: tea.KeyRight, Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlRight}},
		"ctrl+shift+left": {tea.KeyPressMsg{Code: tea.KeyLeft, Mod: ctrl | shift}, teav1.KeyMsg{Type: teav1.KeyCtrlShiftLeft}},
		"shift+home":      {tea.KeyPressMsg{Code: tea.KeyHome, Mod: shift}, teav1.KeyMsg{Type: teav1.KeyShiftHome}},
		"ctrl+end":        {tea.KeyPressMsg{Code: tea.KeyEnd, Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlEnd}},
		// v1 has no shifted page keys, so shift is dropped.
		"shift+pgup":     {tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: shift}, teav1.KeyMsg{Type: teav1.KeyPgUp}},
		"ctrl+pgdown":    {tea.KeyPressMsg{Code: tea.KeyPgDown, Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlPgDown}},
		"enter":          {tea.KeyPressMsg{Code: tea.KeyEnter}, teav1.KeyMsg{Type: teav1.KeyEnter}},
		"tab":            {tea.KeyPressMsg{Code: tea.KeyTab}, teav1.KeyMsg{Type: teav1.KeyTab}},
		"shift+tab":      {tea.KeyPressMsg{Code: tea.KeyTab, Mod: shift}, teav1.KeyMsg{Type: teav1.KeyShiftTab}},
		"backspace":      {tea.KeyPressMsg{Code: tea.KeyBackspace}, teav1.KeyMsg{Type: teav1.KeyBackspace}},
		"alt+backspace":  {tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: alt}, teav1.KeyMsg{Type: teav1.KeyBackspace, Alt: true}},
		"escape":         {tea.KeyPressMsg{Code: tea.KeyEscape}, teav1.KeyMsg{Type: teav1.KeyEscape}},
		"delete":         {tea.KeyPressMsg{Code: tea.KeyDelete}, teav1.KeyMsg{Type: teav1.KeyDelete}},
		"insert":         {tea.KeyPressMsg{Code: tea.KeyInsert}, teav1.KeyMsg{Type: teav1.KeyInsert}},
		"f1":             {tea.KeyPressMsg{Code: tea.KeyF1}, teav1.KeyMsg{Type: teav1.KeyF1}},
		"f5":             {tea.KeyPressMsg{Code: tea.KeyF5}, teav1.KeyMsg{Type: teav1.KeyF5}},
		"f12":            {tea.KeyPressMsg{Code: tea.KeyF12}, teav1.KeyMsg{Type: teav1.KeyF12}},
		"alt+f20":        {tea.KeyPressMsg{Code: tea.KeyF20, Mod: alt}, teav1.KeyMsg{Type: teav1.KeyF20, Alt: true}},
		"ctrl+a":         {tea.KeyPressMsg{Code: 'a', Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlA}},
		"ctrl+w":         {tea.KeyPressMsg{Code: 'w', Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlW}},
		"ctrl+z":         {tea.KeyPressMsg{Code: 'z', Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlZ}},
		"ctrl+alt+c":     {tea.KeyPressMsg{Code: 'c', Mod: ctrl | alt}, teav1.KeyMsg{Type: teav1.KeyCtrlC, Alt: true}},
		"ctrl+space":     {tea.KeyPressMsg{Code: tea.KeySpace, Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlAt}},
		"ctrl+@":         {tea.KeyPressMsg{Code: '@', Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlAt}},
		"ctrl+[":         {tea.KeyPressMsg{Code: '[', Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlOpenBracket}},
		"ctrl+backslash": {tea.KeyPressMsg{Code: '\\', Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlBackslash}},
		"ctrl+]":         {tea.KeyPressMsg{Code: ']', Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlCloseBracket}},
		"ctrl+^":         {tea.KeyPressMsg{Code: '^', Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlCaret}},
		"ctrl+_":         {tea.KeyPressMsg{Code: '_', Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlUnderscore}},
		"ctrl+?":         {tea.KeyPressMsg{Code: '?', Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyCtrlQuestionMark}},
		// A ctrl chord with no control character falls through to the rune.
		"ctrl+1":          {tea.KeyPressMsg{Code: '1', Mod: ctrl}, teav1.KeyMsg{Type: teav1.KeyRunes, Runes: []rune{'1'}}},
		"plain rune":      {tea.KeyPressMsg{Code: 'x', Text: "x"}, teav1.KeyMsg{Type: teav1.KeyRunes, Runes: []rune{'x'}}},
		"shifted rune":    {tea.KeyPressMsg{Code: 'x', Text: "X", Mod: shift}, teav1.KeyMsg{Type: teav1.KeyRunes, Runes: []rune{'X'}}},
		"space":           {tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, teav1.KeyMsg{Type: teav1.KeyRunes, Runes: []rune{' '}}},
		"multi-rune text": {tea.KeyPressMsg{Code: tea.KeyExtended, Text: "日本"}, teav1.KeyMsg{Type: teav1.KeyRunes, Runes: []rune("日本")}},
		// alt+x carries no Text; the base rune is forwarded with Alt set.
		"alt+x":        {tea.KeyPressMsg{Code: 'x', Mod: alt}, teav1.KeyMsg{Type: teav1.KeyRunes, Runes: []rune{'x'}, Alt: true}},
		"unmapped key": {tea.KeyPressMsg{Code: tea.KeyMediaPlay}, teav1.KeyMsg{Type: teav1.KeyRunes}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := toEmulatorMsg(test.in); !reflect.DeepEqual(got, teav1.Msg(test.want)) {
				t.Fatalf("toEmulatorMsg(%#v) = %#v, want %#v", test.in, got, test.want)
			}
		})
	}
}

// The F-key arithmetic assumes both versions number F1..F20 contiguously.
func TestToEmulatorMsgMapsEveryFunctionKey(t *testing.T) {
	want := []teav1.KeyType{
		teav1.KeyF1, teav1.KeyF2, teav1.KeyF3, teav1.KeyF4, teav1.KeyF5,
		teav1.KeyF6, teav1.KeyF7, teav1.KeyF8, teav1.KeyF9, teav1.KeyF10,
		teav1.KeyF11, teav1.KeyF12, teav1.KeyF13, teav1.KeyF14, teav1.KeyF15,
		teav1.KeyF16, teav1.KeyF17, teav1.KeyF18, teav1.KeyF19, teav1.KeyF20,
	}
	in := []rune{
		tea.KeyF1, tea.KeyF2, tea.KeyF3, tea.KeyF4, tea.KeyF5,
		tea.KeyF6, tea.KeyF7, tea.KeyF8, tea.KeyF9, tea.KeyF10,
		tea.KeyF11, tea.KeyF12, tea.KeyF13, tea.KeyF14, tea.KeyF15,
		tea.KeyF16, tea.KeyF17, tea.KeyF18, tea.KeyF19, tea.KeyF20,
	}
	for i, code := range in {
		got := toEmulatorMsg(tea.KeyPressMsg{Code: code})
		if !reflect.DeepEqual(got, teav1.Msg(teav1.KeyMsg{Type: want[i]})) {
			t.Fatalf("F%d = %#v, want type %v", i+1, got, want[i])
		}
	}
}

func TestToEmulatorMsgConvertsPasteAndMouse(t *testing.T) {
	for name, test := range map[string]struct {
		in   tea.Msg
		want teav1.Msg
	}{
		"paste": {
			tea.PasteMsg{Content: "a\nb"},
			teav1.KeyMsg{Type: teav1.KeyRunes, Runes: []rune("a\nb"), Paste: true},
		},
		"left click": {
			tea.MouseClickMsg{X: 3, Y: 4, Button: tea.MouseLeft},
			teav1.MouseMsg{X: 3, Y: 4, Button: teav1.MouseButtonLeft, Action: teav1.MouseActionPress},
		},
		"modified right click": {
			tea.MouseClickMsg{X: 1, Y: 2, Button: tea.MouseRight, Mod: tea.ModShift | tea.ModAlt | tea.ModCtrl},
			teav1.MouseMsg{X: 1, Y: 2, Button: teav1.MouseButtonRight, Action: teav1.MouseActionPress, Shift: true, Alt: true, Ctrl: true},
		},
		"middle release": {
			tea.MouseReleaseMsg{X: 5, Y: 6, Button: tea.MouseMiddle},
			teav1.MouseMsg{X: 5, Y: 6, Button: teav1.MouseButtonMiddle, Action: teav1.MouseActionRelease},
		},
		"drag": {
			tea.MouseMotionMsg{X: 7, Y: 8, Button: tea.MouseLeft},
			teav1.MouseMsg{X: 7, Y: 8, Button: teav1.MouseButtonLeft, Action: teav1.MouseActionMotion},
		},
		"hover": {
			tea.MouseMotionMsg{X: 7, Y: 8},
			teav1.MouseMsg{X: 7, Y: 8, Button: teav1.MouseButtonNone, Action: teav1.MouseActionMotion},
		},
		// v1 reports wheel movement as a press of the wheel button.
		"wheel up": {
			tea.MouseWheelMsg{X: 9, Y: 1, Button: tea.MouseWheelUp},
			teav1.MouseMsg{X: 9, Y: 1, Button: teav1.MouseButtonWheelUp, Action: teav1.MouseActionPress},
		},
		"wheel down": {
			tea.MouseWheelMsg{Button: tea.MouseWheelDown},
			teav1.MouseMsg{Button: teav1.MouseButtonWheelDown, Action: teav1.MouseActionPress},
		},
		"wheel left": {
			tea.MouseWheelMsg{Button: tea.MouseWheelLeft},
			teav1.MouseMsg{Button: teav1.MouseButtonWheelLeft, Action: teav1.MouseActionPress},
		},
		"wheel right": {
			tea.MouseWheelMsg{Button: tea.MouseWheelRight},
			teav1.MouseMsg{Button: teav1.MouseButtonWheelRight, Action: teav1.MouseActionPress},
		},
		"backward": {
			tea.MouseClickMsg{Button: tea.MouseBackward},
			teav1.MouseMsg{Button: teav1.MouseButtonBackward, Action: teav1.MouseActionPress},
		},
		"forward": {
			tea.MouseClickMsg{Button: tea.MouseForward},
			teav1.MouseMsg{Button: teav1.MouseButtonForward, Action: teav1.MouseActionPress},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := toEmulatorMsg(test.in); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("toEmulatorMsg(%#v) = %#v, want %#v", test.in, got, test.want)
			}
		})
	}
}

// Portalis lifecycle messages are not input and must reach the emulator as-is.
func TestToEmulatorMsgPassesOtherMessagesThrough(t *testing.T) {
	type lifecycle struct{ n int }
	if got := toEmulatorMsg(lifecycle{n: 7}); got != (lifecycle{n: 7}) {
		t.Fatalf("toEmulatorMsg rewrote a non-input message: %#v", got)
	}
}
