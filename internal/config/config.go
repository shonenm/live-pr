// Package config loads live-pr settings: a global file plus an optional
// per-repo override.
package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config holds user-tunable settings.
type Config struct {
	// Reviewer is the command template launched to review a commit. Placeholders
	// {sha} {base} {head} are substituted; it is run through `sh -c`.
	Reviewer string `toml:"reviewer"`
}

// Default returns built-in settings (delegates diff review to codediff/nvim).
func Default() Config {
	return Config{Reviewer: `nvim -c "CodeDiff {sha}~1 {sha}"`}
}

// Load returns Default overlaid with the global config, then the per-repo
// config — later files override fields they set. Missing files are ignored.
func Load(repoRoot string) Config {
	cfg := Default()
	paths := []string{
		filepath.Join(globalConfigDir(), "live-pr", "config.toml"),
		filepath.Join(repoRoot, ".live-pr", "config.toml"),
	}
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil {
			_ = toml.Unmarshal(data, &cfg) // only present keys override
		}
	}
	return cfg
}

// globalConfigDir honors XDG_CONFIG_HOME, falling back to ~/.config.
func globalConfigDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}
