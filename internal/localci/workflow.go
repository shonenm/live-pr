// Package localci runs GitHub Actions workflow jobs in the local checkout and
// records signed attestations that a remote verification step can trust.
package localci

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Step is one locally executable workflow step.
type Step struct {
	Name  string
	Run   string
	Shell string
	Dir   string
	Env   []string
}

// Job is a workflow job resolved for local execution. A job with a non-empty
// Unsupported reason stays remote-only.
type Job struct {
	ID          string
	Name        string
	Steps       []Step
	Unsupported string
}

type workflowFile struct {
	Env      map[string]string `yaml:"env"`
	Defaults yaml.Node         `yaml:"defaults"`
	Jobs     yaml.Node         `yaml:"jobs"`
}

type workflowJob struct {
	Name      string            `yaml:"name"`
	If        string            `yaml:"if"`
	Env       map[string]string `yaml:"env"`
	Strategy  yaml.Node         `yaml:"strategy"`
	Services  yaml.Node         `yaml:"services"`
	Container yaml.Node         `yaml:"container"`
	Defaults  yaml.Node         `yaml:"defaults"`
	Steps     []workflowStep    `yaml:"steps"`
}

type workflowStep struct {
	Name  string            `yaml:"name"`
	If    string            `yaml:"if"`
	Uses  string            `yaml:"uses"`
	Run   string            `yaml:"run"`
	Shell string            `yaml:"shell"`
	Dir   string            `yaml:"working-directory"`
	Env   map[string]string `yaml:"env"`
}

// Load resolves the requested jobs of a workflow file. `run:` steps execute
// verbatim; a `uses:` step runs its replacement from uses (keyed by action
// name without the @ref), and actions/checkout is satisfied by the checkout
// itself. Anything that cannot be reproduced locally marks the job
// Unsupported rather than being approximated.
func Load(root, path string, jobIDs []string, uses map[string]string) ([]Job, error) {
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return nil, err
	}
	var wf workflowFile
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	defined := map[string]workflowJob{}
	for i := 0; i+1 < len(wf.Jobs.Content); i += 2 {
		var job workflowJob
		if err := wf.Jobs.Content[i+1].Decode(&job); err != nil {
			return nil, fmt.Errorf("parse %s job %s: %w", path, wf.Jobs.Content[i].Value, err)
		}
		defined[wf.Jobs.Content[i].Value] = job
	}
	jobs := make([]Job, 0, len(jobIDs))
	for _, id := range jobIDs {
		src, ok := defined[id]
		if !ok {
			return nil, fmt.Errorf("%s has no job %q", path, id)
		}
		job := Job{ID: id, Name: src.Name}
		if job.Name == "" {
			job.Name = id
		}
		job.Steps, job.Unsupported = resolveSteps(wf, src, uses)
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func resolveSteps(wf workflowFile, job workflowJob, uses map[string]string) ([]Step, string) {
	switch {
	case job.If != "":
		return nil, "job has an if condition"
	case !job.Strategy.IsZero():
		return nil, "job uses a strategy matrix"
	case !job.Services.IsZero():
		return nil, "job uses services"
	case !job.Container.IsZero():
		return nil, "job uses a container"
	case !wf.Defaults.IsZero() || !job.Defaults.IsZero():
		return nil, "workflow uses defaults"
	}
	var steps []Step
	for i, src := range job.Steps {
		step := Step{Name: src.Name, Run: src.Run, Shell: src.Shell, Dir: src.Dir}
		label := src.Name
		if src.Uses != "" {
			action, _, _ := strings.Cut(src.Uses, "@")
			if label == "" {
				label = action
			}
			if action == "actions/checkout" {
				continue
			}
			replacement, ok := uses[action]
			if !ok {
				return nil, fmt.Sprintf("step %q has no [ci.local.uses] replacement", label)
			}
			step.Run, step.Shell, step.Dir = replacement, "", ""
		}
		if label == "" {
			label, _, _ = strings.Cut(strings.TrimSpace(step.Run), "\n")
		}
		step.Name = label
		if step.Run == "" {
			return nil, fmt.Sprintf("step %d has neither run nor uses", i+1)
		}
		if src.If != "" {
			return nil, fmt.Sprintf("step %q has an if condition", label)
		}
		if step.Shell != "" && step.Shell != "bash" && step.Shell != "sh" {
			return nil, fmt.Sprintf("step %q uses shell %q", label, step.Shell)
		}
		for _, env := range []map[string]string{wf.Env, job.Env, src.Env} {
			for key, value := range env {
				if strings.Contains(value, "${{") {
					return nil, fmt.Sprintf("step %q env %s uses an expression", label, key)
				}
				step.Env = append(step.Env, key+"="+value)
			}
		}
		if src.Uses == "" && strings.Contains(step.Run, "${{") {
			return nil, fmt.Sprintf("step %q uses an expression", label)
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return nil, "job has no local steps"
	}
	return steps, ""
}
