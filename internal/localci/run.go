// Package localci runs a repository's check commands in the local checkout
// and records signed attestations that a remote verification step can trust.
package localci

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sort"
	"time"
)

// Step is one unit of progress inside a job.
type Step struct {
	Name string
	Run  string
}

// Job is a named check. Its ID is the name the remote workflow gates on.
type Job struct {
	ID    string
	Steps []Step
}

// Jobs turns the configured `name = command` checks into jobs, sorted by
// name. The command is the repository's own entry point (`just check`,
// `bin/ci`), which the remote job is expected to call as well.
// ponytail: one step per check; split a command's output on `::group::`
// markers when step-level progress is wanted.
func Jobs(checks map[string]string) []Job {
	jobs := make([]Job, 0, len(checks))
	for name, command := range checks {
		jobs = append(jobs, Job{ID: name, Steps: []Step{{Name: command, Run: command}}})
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	return jobs
}

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

// Run executes the jobs in order inside root and returns the IDs of the jobs
// whose steps all succeeded. Steps after a failure are skipped.
func Run(ctx context.Context, root string, jobs []Job, emit func(Event)) []string {
	var passed []string
	for _, job := range jobs {
		ok := true
		for i, step := range job.Steps {
			if !ok {
				emit(Event{Job: job.ID, Step: i, State: StateSkipped})
				continue
			}
			emit(Event{Job: job.ID, Step: i, State: StateRunning})
			start := time.Now()
			cmd := exec.CommandContext(ctx, "sh", "-c", step.Run)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "CI=true")
			out, err := cmd.CombinedOutput()
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
