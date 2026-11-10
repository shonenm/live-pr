package localci

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Step states reported through Event.
const (
	StateRunning = "running"
	StateSuccess = "success"
	StateFailure = "failure"
	StateSkipped = "skipped"
)

// Event reports a step state change. Output carries the tail of a failed
// step's combined output.
type Event struct {
	Job      string
	Step     int
	State    string
	Duration time.Duration
	Output   string
}

const outputTailBytes = 4096

// Run executes the supported jobs in order inside root and returns the IDs of
// the jobs whose steps all succeeded. Steps after a failure are skipped, as
// on GitHub Actions.
func Run(ctx context.Context, root string, jobs []Job, emit func(Event)) []string {
	var passed []string
	for _, job := range jobs {
		if job.Unsupported != "" {
			continue
		}
		ok := true
		for i, step := range job.Steps {
			if !ok {
				emit(Event{Job: job.ID, Step: i, State: StateSkipped})
				continue
			}
			emit(Event{Job: job.ID, Step: i, State: StateRunning})
			start := time.Now()
			out, err := runStep(ctx, root, step)
			ev := Event{Job: job.ID, Step: i, State: StateSuccess, Duration: time.Since(start).Truncate(time.Second)}
			if err != nil {
				ok = false
				ev.State = StateFailure
				if len(out) > outputTailBytes {
					out = out[len(out)-outputTailBytes:]
				}
				ev.Output = string(bytes.TrimSpace(out))
				if ev.Output == "" {
					ev.Output = err.Error()
				}
			}
			emit(ev)
		}
		if ok {
			passed = append(passed, job.ID)
		}
	}
	return passed
}

func runStep(ctx context.Context, root string, step Step) ([]byte, error) {
	// Mirror the GitHub Actions shell templates so `run:` text behaves the
	// same locally: unspecified is `bash -e`, explicit bash adds pipefail.
	args := []string{"bash", "-e", "-c", step.Run}
	switch step.Shell {
	case "bash":
		args = []string{"bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", step.Run}
	case "sh":
		args = []string{"sh", "-e", "-c", step.Run}
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = filepath.Join(root, step.Dir)
	cmd.Env = append(append(os.Environ(), "CI=true"), step.Env...)
	return cmd.CombinedOutput()
}
