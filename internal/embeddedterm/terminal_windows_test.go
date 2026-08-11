//go:build windows

package embeddedterm

import "testing"

func TestWindowsFallback(t *testing.T) {
	if New("", "", nil) != nil {
		t.Fatal("empty command should disable embedded review")
	}
	term := New("nvim", "", nil)
	if term == nil || term.Available() || term.Err() == nil {
		t.Fatalf("unexpected Windows fallback: %#v", term)
	}
	msg := term.Init()()
	if !term.Handles(msg) {
		t.Fatalf("terminal does not own its state message: %#v", msg)
	}
	if term.Handles(StateMsg{SessionID: "other"}) {
		t.Fatal("terminal accepted another session")
	}
}
