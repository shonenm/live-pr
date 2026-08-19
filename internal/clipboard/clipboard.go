// Package clipboard writes text to the user's clipboard, preferring native
// tools and falling back to the OSC 52 terminal escape.
package clipboard

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Write copies text to the clipboard. Native tools are tried first because they
// work regardless of terminal support; OSC 52 covers headless SSH sessions,
// where it reaches the user's real terminal through tmux.
func Write(text string) error {
	if err := native(text); err == nil {
		return nil
	}
	return osc52(text)
}

func native(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	default:
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else {
			return errors.New("no clipboard tool")
		}
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// osc52 writes to stderr so the sequence reaches the terminal without
// interleaving with the TUI's stdout rendering.
func osc52(text string) error {
	return writeOSC52(os.Stderr, text)
}

// writeOSC52 emits the OSC 52 clipboard escape for text on w.
func writeOSC52(w io.Writer, text string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	_, err := fmt.Fprintf(w, "\x1b]52;c;%s\x07", encoded)
	return err
}
