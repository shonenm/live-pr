package tui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	gh "github.com/shonenm/live-pr/internal/github"
)

type ciCommandDone struct {
	generation uint64
	output     string
	steps      []woodpeckerStep
	err        error
}

type woodpeckerStep struct {
	workflow, name, state, duration string
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

func runWoodpeckerCI(root, repository, server string, cliCommand, tokenCommand []string, pr gh.PR, generation uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		env := os.Environ()
		if server != "" {
			env = replaceEnv(env, "WOODPECKER_SERVER", server)
		}
		if len(tokenCommand) > 0 {
			token, err := readCIToken(ctx, root, tokenCommand)
			if err != nil {
				return ciCommandDone{generation: generation, err: err}
			}
			env = replaceEnv(env, "WOODPECKER_TOKEN", token)
		}

		repository = woodpeckerRepository(repository, pr.URL)
		if repository == "" {
			return ciCommandDone{generation: generation, err: errors.New("Woodpecker repository is unavailable")}
		}
		output, steps, err := loadWoodpeckerCI(ctx, root, env, cliCommand, repository, pr.HeadRefOID)
		return ciCommandDone{generation: generation, output: output, steps: steps, err: err}
	}
}

func readCIToken(ctx context.Context, root string, command []string) (string, error) {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("CI token command timed out")
		}
		return "", errors.New("CI token command failed")
	}
	token := strings.TrimSpace(string(out))
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return "", errors.New("CI token command must return one non-empty line")
	}
	return token, nil
}

func loadWoodpeckerCI(ctx context.Context, root string, env, cliCommand []string, repository, headSHA string) (string, []woodpeckerStep, error) {
	listTemplate := "go-format={{.Number}}\t{{.Commit}}\t{{.Status}}"
	out, err := runWoodpeckerCLI(ctx, root, env, cliCommand, "pipeline", "ls", repository, "--limit", "100", "--output", listTemplate)
	if err != nil {
		return "", nil, err
	}

	number, pipelineState := findWoodpeckerPipeline(string(out), headSHA)
	if number == "" {
		return "Woodpecker\n  (no pipeline for this commit)", nil, nil
	}

	stepTemplate := "{{.workflow.Name}}\t{{.step.Name}}\t{{.step.State}}\t{{.step.Started}}\t{{.step.Stopped}}"
	out, err = runWoodpeckerCLI(ctx, root, env, cliCommand, "pipeline", "ps", repository, number, "--format", stepTemplate)
	if err != nil {
		return "", nil, err
	}
	return formatWoodpeckerCI(number, pipelineState, string(out)), parseWoodpeckerSteps(string(out)), nil
}

func runWoodpeckerCLI(ctx context.Context, root string, env, command []string, args ...string) ([]byte, error) {
	if len(command) == 0 {
		command = []string{"woodpecker-cli"}
	}
	cmd := exec.CommandContext(ctx, command[0], append(command[1:], args...)...)
	cmd.Dir, cmd.Env = root, env
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("woodpecker-cli timed out")
		}
		return nil, fmt.Errorf("woodpecker-cli failed: %w", err)
	}
	return out, nil
}

func findWoodpeckerPipeline(output, headSHA string) (string, string) {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 3 && fields[1] == headSHA {
			return fields[0], fields[2]
		}
	}
	return "", ""
}

func parseWoodpeckerSteps(steps string) []woodpeckerStep {
	var out []woodpeckerStep
	for _, row := range strings.Split(strings.TrimSpace(steps), "\n") {
		fields := strings.Split(row, "\t")
		if len(fields) != 5 {
			continue
		}
		step := woodpeckerStep{workflow: fields[0], name: fields[1], state: fields[2]}
		if duration := woodpeckerDuration(fields[3], fields[4]); duration != "" {
			step.duration = duration
		}
		out = append(out, step)
	}
	return out
}

func formatWoodpeckerCI(number, pipelineState, steps string) string {
	icon, _, style := commitCIStatus(woodpeckerStatus(pipelineState))
	lines := []string{iconLine("", style, icon, "Woodpecker #"+number, pipelineState, "")}
	lastWorkflow := ""
	for _, step := range parseWoodpeckerSteps(steps) {
		if step.workflow != lastWorkflow {
			lines = append(lines, stFg.Bold(true).Render(step.workflow))
			lastWorkflow = step.workflow
		}
		lines = append(lines, woodpeckerStepLine("  └─ ", step))
	}
	return strings.Join(lines, "\n")
}

func woodpeckerStepLine(indent string, step woodpeckerStep) string {
	icon, _, style := commitCIStatus(woodpeckerStatus(step.state))
	return iconLine(indent, style, icon, step.name, step.state, step.duration)
}

func woodpeckerStatus(state string) string {
	switch strings.ToLower(state) {
	case "success":
		return "SUCCESS"
	case "failure", "error", "killed", "declined":
		return "FAILURE"
	case "pending", "running", "blocked", "created":
		return "PENDING"
	default:
		return ""
	}
}

func woodpeckerWorkflowKey(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func woodpeckerDuration(started, stopped string) string {
	start, err := strconv.ParseInt(started, 10, 64)
	if err != nil || start == 0 {
		return ""
	}
	end, err := strconv.ParseInt(stopped, 10, 64)
	if err != nil || end == 0 {
		return ""
	}
	return (time.Duration(end-start) * time.Second).String()
}

func woodpeckerRepository(repository, prURL string) string {
	if repository != "" {
		return repository
	}
	u, err := url.Parse(prURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}
