// Package config loads live-pr settings: a global file plus an optional
// per-repo override.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config holds user-tunable settings.
type Config struct {
	// Reviewer is the legacy commit template used when diff.commit_command is
	// absent. {sha}/{base}/{head} map to the corresponding LIVE_PR_* values.
	Reviewer string `toml:"reviewer"`

	// SummaryMinIntervalMinutes throttles the Stop-hook summarizer: no new
	// summary is added within this window of the previous one (0 = no throttle).
	SummaryMinIntervalMinutes int `toml:"summary_min_interval_minutes"`

	// SummarizeModel optionally overrides the model used for summarization.
	SummarizeModel string `toml:"summarize_model"`

	// Diff controls the optional right-pane diff display filter.
	Diff DiffConfig `toml:"diff"`
}

// DiffConfig customizes how raw Git diff is rendered in the right pane.
type DiffConfig struct {
	// Command runs an interactive reviewer in the right pane's embedded PTY.
	// It receives LIVE_PR_RANGE, LIVE_PR_BASE, LIVE_PR_HEAD,
	// LIVE_PR_HEAD_REV, LIVE_PR_PR_URL, and LIVE_PR_SHA.
	Command string `toml:"command"`

	// CommitCommand runs the embedded reviewer for LIVE_PR_SHA.
	CommitCommand string `toml:"commit_command"`

	// Display receives raw diff on stdin and writes ANSI text to stdout when no
	// interactive command is configured. Empty keeps the built-in Git output.
	Display string `toml:"display"`
}

// Default returns built-in settings, including legacy commit compatibility.
func Default() Config {
	return Config{
		Reviewer:                  `nvim -c "CodeDiff {sha}~1 {sha}"`,
		SummaryMinIntervalMinutes: 10,
		Diff: DiffConfig{
			Command: `nvim -c "CodeDiff --inline $LIVE_PR_RANGE"`,
		},
	}
}

// CommitReviewCommand returns the embedded commit command, falling back to
// the legacy reviewer template for existing configurations.
func (c Config) CommitReviewCommand() string {
	if c.Diff.CommitCommand != "" {
		return c.Diff.CommitCommand
	}
	return strings.NewReplacer(
		"{sha}", "$LIVE_PR_SHA",
		"{base}", "$LIVE_PR_BASE",
		"{head}", "$LIVE_PR_HEAD",
	).Replace(c.Reviewer)
}

// SummaryInterval is the throttle window as a duration.
func (c Config) SummaryInterval() time.Duration {
	return time.Duration(c.SummaryMinIntervalMinutes) * time.Minute
}

// Load returns Default overlaid with the global config, then the per-repo
// config — later files override fields they set. Missing files are ignored.
func Load(repoRoot string) (Config, error) {
	cfg := Default()
	paths := []string{filepath.Join(globalConfigDir(), "live-pr", "config.toml")}
	repoConfig := filepath.Join(repoRoot, ".live-pr.toml")
	if _, err := os.Stat(repoConfig); os.IsNotExist(err) {
		// Read the old location only as a migration path. It is no longer
		// created, so a normal run leaves no .live-pr/ directory behind.
		repoConfig = filepath.Join(repoRoot, ".live-pr", "config.toml")
	}
	paths = append(paths, repoConfig)
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return Config{}, fmt.Errorf("read config %s: %w", p, err)
		}
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", p, err)
		}
	}
	// Migrate the old built-in command while preserving explicit custom commands.
	if cfg.Diff.Command == `nvim -c "CodeDiff $LIVE_PR_RANGE"` {
		cfg.Diff.Command = `nvim -c "CodeDiff --inline $LIVE_PR_RANGE"`
	}
	return cfg, nil
}

// globalConfigDir honors XDG_CONFIG_HOME, falling back to ~/.config.
func globalConfigDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}
