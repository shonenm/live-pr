// Package config loads live-pr settings: a global file plus an optional
// per-repo override.
package config

import (
	"errors"
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

	// SummarizeCommand replaces the built-in `claude -p` summarizer with an
	// arbitrary shell command (run via `sh -c`). Contract: the session
	// transcript arrives on stdin; the summary goes to stdout as a title on
	// the first non-empty line followed by optional detail; empty output
	// records nothing. Empty keeps `claude -p`.
	SummarizeCommand string `toml:"summarize_command"`

	// Theme selects the TUI color palette. One of: "primer-dark" (default),
	// "primer-light", "nord", "catppuccin-mocha". Unknown values fall back
	// to primer-dark.
	Theme string `toml:"theme"`

	// Accessibility controls low-color and limited-glyph terminal output.
	Accessibility AccessibilityConfig `toml:"accessibility"`

	// Diff controls the optional right-pane diff display filter.
	Diff DiffConfig `toml:"diff"`

	// List controls the PR list screen.
	List ListConfig `toml:"list"`

	// Merge controls the merge confirmation picker.
	Merge MergeConfig `toml:"merge"`

	// CI controls optional extra content in the CI checks view.
	CI CIConfig `toml:"ci"`

	// Views are the PR list tabs, in display order. A config that sets any
	// view replaces the built-in set entirely, so tabs can be removed,
	// renamed, reordered, or added.
	Views []View `toml:"views"`
}

// View is one PR list tab: a display name and the GitHub search query behind
// it. The open/closed bucket is read from the query, so "is:closed" is all
// that separates a closed tab from an open one.
type View struct {
	Name  string `toml:"name"`
	Query string `toml:"query"`
}

// Closed reports whether the view lists closed pull requests.
func (v View) Closed() bool {
	for _, token := range strings.Fields(strings.ToLower(v.Query)) {
		key, value, ok := strings.Cut(token, ":")
		if !ok || (key != "is" && key != "state") {
			continue
		}
		switch value {
		case "closed":
			return true
		case "open":
			return false
		}
	}
	return false
}

// DefaultViews are the tabs shipped with live-pr.
func DefaultViews() []View {
	return []View{
		{Name: "Assigned", Query: "assignee:@me"},
		{Name: "Review requested", Query: "review-requested:@me"},
		{Name: "All", Query: ""},
		{Name: "Authored", Query: "author:@me"},
		{Name: "Needs me", Query: "(assignee:@me OR review-requested:@me)"},
		{Name: "Closed", Query: "is:closed"},
	}
}

// NormalizeViews drops unusable entries and makes names unique, since a view
// keys its cache by name. An empty result falls back to the built-in set:
// a PR list with no tabs has nothing to show.
func NormalizeViews(views []View) []View {
	seen := map[string]bool{}
	result := make([]View, 0, len(views))
	for _, view := range views {
		view.Name = strings.TrimSpace(view.Name)
		view.Query = strings.TrimSpace(view.Query)
		if view.Name == "" {
			continue
		}
		key := strings.ToLower(view.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, view)
	}
	if len(result) == 0 {
		return DefaultViews()
	}
	return result
}

// AccessibilityConfig adapts rendering for limited terminals.
type AccessibilityConfig struct {
	Monochrome bool `toml:"monochrome"`
	ASCII      bool `toml:"ascii"`
}

// ListConfig customizes the PR list screen.
type ListConfig struct {
	// SplitRatio is the list pane's share in percent on the PR list screen.
	// It is independent of diff.split_ratio, which shapes the detail screen.
	SplitRatio int `toml:"split_ratio"`
}

// MergeConfig customizes the merge confirmation picker.
type MergeConfig struct {
	// Methods lists merge, squash, and rebase in display order. The first
	// method is selected by default.
	Methods []string `toml:"methods"`
}

// CIConfig customizes the CI checks view.
type CIConfig struct {
	// Command runs when the checks view is opened. Its stdout is appended to
	// the GitHub checks, allowing custom CI details to be rendered.
	Command string `toml:"command"`

	// Provider enables a built-in CI integration. Currently: "woodpecker".
	Provider string `toml:"provider"`

	// Server, CLICommand, and TokenCommand are authentication settings loaded
	// only from global config. Repository config cannot redirect credentials.
	Server       string   `toml:"server"`
	CLICommand   []string `toml:"cli_command"`
	TokenCommand []string `toml:"token_command"`
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

	// SplitRatio is the left-pane share in percent.
	SplitRatio int `toml:"split_ratio"`

	// MinPaneWidth is the minimum width for either side of a split.
	MinPaneWidth int `toml:"min_pane_width"`
}

// Default returns built-in settings, including legacy commit compatibility.
func Default() Config {
	return Config{
		Reviewer:                  `nvim -c "CodeDiff {sha}~1 {sha}"`,
		SummaryMinIntervalMinutes: 10,
		Diff: DiffConfig{
			Command:      `nvim -c "CodeDiff --inline $LIVE_PR_RANGE"`,
			SplitRatio:   52,
			MinPaneWidth: 24,
		},
		List:  ListConfig{SplitRatio: 45},
		Merge: MergeConfig{Methods: []string{"merge", "squash", "rebase"}},
		Views: DefaultViews(),
	}
}

// ApplyDiffPreset overrides the diff section with a named preset or a raw
// command string. Known presets: git, delta, codediff, codereview.
func (c *Config) ApplyDiffPreset(name string) {
	switch name {
	case "git":
		c.Diff.Command, c.Diff.CommitCommand, c.Diff.Display = "", "", ""
	case "delta":
		c.Diff.Command, c.Diff.CommitCommand = "", ""
		c.Diff.Display = "delta --color-only --paging=never --line-numbers"
	case "codediff", "codereview":
		c.Diff.Command = `nvim -c "CodeDiff --inline $LIVE_PR_RANGE"`
		c.Diff.CommitCommand = `nvim -c "CodeDiff $LIVE_PR_SHA~1 $LIVE_PR_SHA"`
		c.Diff.Display = ""
	default:
		c.Diff.Command = name
		c.Diff.CommitCommand = ""
		c.Diff.Display = ""
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
	globalPath := filepath.Join(globalConfigDir(), "live-pr", "config.toml")
	if err := overlay(globalPath, &cfg); err != nil {
		return Config{}, err
	}
	globalServer := cfg.CI.Server
	globalCLICommand := append([]string(nil), cfg.CI.CLICommand...)
	globalTokenCommand := append([]string(nil), cfg.CI.TokenCommand...)

	repoConfig := filepath.Join(repoRoot, ".live-pr.toml")
	if _, err := os.Stat(repoConfig); os.IsNotExist(err) {
		// Read the old location only as a migration path. It is no longer
		// created, so a normal run leaves no .live-pr/ directory behind.
		repoConfig = filepath.Join(repoRoot, ".live-pr", "config.toml")
	}
	if err := overlay(repoConfig, &cfg); err != nil {
		return Config{}, err
	}
	// Authentication belongs to the user, never to a cloned repository.
	cfg.CI.Server = globalServer
	cfg.CI.CLICommand = globalCLICommand
	cfg.CI.TokenCommand = globalTokenCommand
	// Migrate the old built-in command while preserving explicit custom commands.
	if cfg.Diff.Command == `nvim -c "CodeDiff $LIVE_PR_RANGE"` {
		cfg.Diff.Command = `nvim -c "CodeDiff --inline $LIVE_PR_RANGE"`
	}
	if cfg.Diff.SplitRatio <= 0 {
		cfg.Diff.SplitRatio = Default().Diff.SplitRatio
	}
	if cfg.Diff.MinPaneWidth <= 0 {
		cfg.Diff.MinPaneWidth = Default().Diff.MinPaneWidth
	}
	if cfg.List.SplitRatio <= 0 || cfg.List.SplitRatio >= 100 {
		cfg.List.SplitRatio = Default().List.SplitRatio
	}
	if err := validateMergeMethods(cfg.Merge.Methods); err != nil {
		return Config{}, err
	}
	cfg.Views = NormalizeViews(cfg.Views)
	return cfg, nil
}

func validateMergeMethods(methods []string) error {
	if len(methods) != 3 {
		return errors.New("merge.methods must contain merge, squash, and rebase exactly once")
	}
	seen := make(map[string]bool, len(methods))
	for _, method := range methods {
		if method != "merge" && method != "squash" && method != "rebase" || seen[method] {
			return errors.New("merge.methods must contain merge, squash, and rebase exactly once")
		}
		seen[method] = true
	}
	return nil
}

func overlay(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

// globalConfigDir honors XDG_CONFIG_HOME, falling back to ~/.config.
func globalConfigDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}
