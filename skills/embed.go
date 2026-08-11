// Package skills embeds the Agent Skill shipped with the live-pr binary.
package skills

import _ "embed"

// Markdown is the version-matched live-pr Agent Skill.
//
//go:embed live-pr/SKILL.md
var Markdown string
