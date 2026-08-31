package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	gh "github.com/shonenm/live-pr/internal/github"
)

type ciCommandDone struct {
	generation uint64
	output     string
	err        error
}

func runCICommand(command, root, repository string, pr gh.PR, generation uint64) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("sh", "-c", command)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"LIVE_PR_REPO="+repository,
			"LIVE_PR_PR_NUMBER="+strconv.Itoa(pr.Number),
			"LIVE_PR_PR_URL="+pr.URL,
			"LIVE_PR_HEAD_SHA="+pr.HeadRefOID,
		)
		out, err := cmd.Output()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
				err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
			}
		}
		return ciCommandDone{generation: generation, output: strings.TrimSpace(string(out)), err: err}
	}
}
