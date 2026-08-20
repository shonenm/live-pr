//go:build !windows

// The TUI runs on Bubble Tea v2 while portalis still speaks v1. This file is
// the seam between the two: v2 input messages are rewritten into the v1
// shapes the emulator consumes, and the emulator's v1 commands are wrapped
// back into v2 commands.
package embeddedterm

import (
	tea "charm.land/bubbletea/v2"
	teav1 "github.com/charmbracelet/bubbletea"
)

// wrapCmd lifts a portalis (Bubble Tea v1) command into a v2 command. The
// messages it produces are portalis's own types, which flow through the v2
// program untouched.
func wrapCmd(cmd teav1.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg { return cmd() }
}

// toEmulatorMsg converts v2 key, paste, and mouse input to the v1 messages
// portalis consumes. Everything else (portalis lifecycle messages) passes
// through unchanged.
func toEmulatorMsg(msg tea.Msg) teav1.Msg {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return keyToV1(msg)
	case tea.PasteMsg:
		return teav1.KeyMsg{Type: teav1.KeyRunes, Runes: []rune(msg.Content), Paste: true}
	case tea.MouseClickMsg:
		return mouseToV1(tea.Mouse(msg), teav1.MouseActionPress)
	case tea.MouseReleaseMsg:
		return mouseToV1(tea.Mouse(msg), teav1.MouseActionRelease)
	case tea.MouseMotionMsg:
		return mouseToV1(tea.Mouse(msg), teav1.MouseActionMotion)
	case tea.MouseWheelMsg:
		// v1 reports wheel movement as a press of the wheel button.
		return mouseToV1(tea.Mouse(msg), teav1.MouseActionPress)
	}
	return msg
}

// keyToV1 maps a v2 key press onto the v1 KeyMsg fields portalis encodes into
// PTY bytes: Type, Runes, and Alt.
func keyToV1(msg tea.KeyPressMsg) teav1.KeyMsg {
	alt := msg.Mod.Contains(tea.ModAlt)
	shift := msg.Mod.Contains(tea.ModShift)
	ctrl := msg.Mod.Contains(tea.ModCtrl)

	switch msg.Code {
	case tea.KeyUp:
		return teav1.KeyMsg{Type: modalKeyType(shift, ctrl, teav1.KeyUp, teav1.KeyShiftUp, teav1.KeyCtrlUp, teav1.KeyCtrlShiftUp), Alt: alt}
	case tea.KeyDown:
		return teav1.KeyMsg{Type: modalKeyType(shift, ctrl, teav1.KeyDown, teav1.KeyShiftDown, teav1.KeyCtrlDown, teav1.KeyCtrlShiftDown), Alt: alt}
	case tea.KeyRight:
		return teav1.KeyMsg{Type: modalKeyType(shift, ctrl, teav1.KeyRight, teav1.KeyShiftRight, teav1.KeyCtrlRight, teav1.KeyCtrlShiftRight), Alt: alt}
	case tea.KeyLeft:
		return teav1.KeyMsg{Type: modalKeyType(shift, ctrl, teav1.KeyLeft, teav1.KeyShiftLeft, teav1.KeyCtrlLeft, teav1.KeyCtrlShiftLeft), Alt: alt}
	case tea.KeyHome:
		return teav1.KeyMsg{Type: modalKeyType(shift, ctrl, teav1.KeyHome, teav1.KeyShiftHome, teav1.KeyCtrlHome, teav1.KeyCtrlShiftHome), Alt: alt}
	case tea.KeyEnd:
		return teav1.KeyMsg{Type: modalKeyType(shift, ctrl, teav1.KeyEnd, teav1.KeyShiftEnd, teav1.KeyCtrlEnd, teav1.KeyCtrlShiftEnd), Alt: alt}
	case tea.KeyPgUp:
		return teav1.KeyMsg{Type: modalKeyType(false, ctrl, teav1.KeyPgUp, teav1.KeyPgUp, teav1.KeyCtrlPgUp, teav1.KeyCtrlPgUp), Alt: alt}
	case tea.KeyPgDown:
		return teav1.KeyMsg{Type: modalKeyType(false, ctrl, teav1.KeyPgDown, teav1.KeyPgDown, teav1.KeyCtrlPgDown, teav1.KeyCtrlPgDown), Alt: alt}
	case tea.KeyEnter:
		return teav1.KeyMsg{Type: teav1.KeyEnter, Alt: alt}
	case tea.KeyTab:
		if shift {
			return teav1.KeyMsg{Type: teav1.KeyShiftTab, Alt: alt}
		}
		return teav1.KeyMsg{Type: teav1.KeyTab, Alt: alt}
	case tea.KeyBackspace:
		return teav1.KeyMsg{Type: teav1.KeyBackspace, Alt: alt}
	case tea.KeyEscape:
		return teav1.KeyMsg{Type: teav1.KeyEscape, Alt: alt}
	case tea.KeyDelete:
		return teav1.KeyMsg{Type: teav1.KeyDelete, Alt: alt}
	case tea.KeyInsert:
		return teav1.KeyMsg{Type: teav1.KeyInsert, Alt: alt}
	}
	if msg.Code >= tea.KeyF1 && msg.Code <= tea.KeyF20 {
		// v1 special keys count downward (KeyF2 == KeyF1 - 1).
		return teav1.KeyMsg{Type: teav1.KeyF1 - teav1.KeyType(msg.Code-tea.KeyF1), Alt: alt}
	}
	if ctrl {
		if t, ok := ctrlKeyType(msg.Code); ok {
			return teav1.KeyMsg{Type: t, Alt: alt}
		}
	}
	if msg.Text != "" {
		return teav1.KeyMsg{Type: teav1.KeyRunes, Runes: []rune(msg.Text), Alt: alt}
	}
	// A modified printable key (for example alt+x) carries no Text; forward
	// the base rune so the emulator can apply the Alt prefix itself.
	if msg.Code > 0 && msg.Code < tea.KeyExtended {
		return teav1.KeyMsg{Type: teav1.KeyRunes, Runes: []rune{msg.Code}, Alt: alt}
	}
	return teav1.KeyMsg{Type: teav1.KeyRunes, Alt: alt}
}

func modalKeyType(shift, ctrl bool, base, shifted, ctrled, both teav1.KeyType) teav1.KeyType {
	switch {
	case shift && ctrl:
		return both
	case ctrl:
		return ctrled
	case shift:
		return shifted
	default:
		return base
	}
}

// ctrlKeyType maps a ctrl+key code to its v1 control-character key type
// (0x00–0x1f plus DEL), which portalis writes to the PTY verbatim.
func ctrlKeyType(code rune) (teav1.KeyType, bool) {
	switch {
	case code >= 'a' && code <= 'z':
		return teav1.KeyType(code - 'a' + 1), true
	case code == '@' || code == ' ':
		return teav1.KeyCtrlAt, true
	case code == '[':
		return teav1.KeyCtrlOpenBracket, true
	case code == '\\':
		return teav1.KeyCtrlBackslash, true
	case code == ']':
		return teav1.KeyCtrlCloseBracket, true
	case code == '^':
		return teav1.KeyCtrlCaret, true
	case code == '_':
		return teav1.KeyCtrlUnderscore, true
	case code == '?':
		return teav1.KeyCtrlQuestionMark, true
	}
	return 0, false
}

func mouseToV1(m tea.Mouse, action teav1.MouseAction) teav1.MouseMsg {
	return teav1.MouseMsg{
		X:      m.X,
		Y:      m.Y,
		Button: buttonToV1(m.Button),
		Action: action,
		Shift:  m.Mod.Contains(tea.ModShift),
		Alt:    m.Mod.Contains(tea.ModAlt),
		Ctrl:   m.Mod.Contains(tea.ModCtrl),
	}
}

func buttonToV1(b tea.MouseButton) teav1.MouseButton {
	switch b {
	case tea.MouseLeft:
		return teav1.MouseButtonLeft
	case tea.MouseMiddle:
		return teav1.MouseButtonMiddle
	case tea.MouseRight:
		return teav1.MouseButtonRight
	case tea.MouseWheelUp:
		return teav1.MouseButtonWheelUp
	case tea.MouseWheelDown:
		return teav1.MouseButtonWheelDown
	case tea.MouseWheelLeft:
		return teav1.MouseButtonWheelLeft
	case tea.MouseWheelRight:
		return teav1.MouseButtonWheelRight
	case tea.MouseBackward:
		return teav1.MouseButtonBackward
	case tea.MouseForward:
		return teav1.MouseButtonForward
	}
	return teav1.MouseButtonNone
}
