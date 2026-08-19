```
╻  ╻╻ ╻┏━╸   ┏━┓┏━┓
┃  ┃┃┏┛┣╸ ╺━╸┣━┛┣┳┛
┗━╸╹┗┛ ┗━╸   ╹  ╹┗╸
```

# live-pr

Living pull request for LLM-assisted development.

![demo](assets/demo.gif)

A GitHub PR records only the compressed final conclusion. The actual
iteration — pivots, discarded approaches, why each decision was made —
happens locally with the coding agent and is thrown away.

live-pr keeps that timeline alive: agents and humans append `decision` /
`pivot` / `note` records during development, you review the branch like a
GitHub PR in a local TUI, and at the end the timeline ships as the body of
a real pull request.

## Features

- Decision timeline — append-only records from humans and agents, editable from the TUI and CLI
- GitHub-PR-style TUI — PR list with views/filters/stacks, and a detail screen with the conversation beside a review pane
- Full conversation sync — PR description, comments, submitted reviews (colored verdicts), inline review comments, and lifecycle activity
- Comment from the TUI — post, edit, and delete GitHub conversation comments; edit the PR description
- Review from the TUI — inline comments plus a verdict (comment / approve / request changes) submitted together
- Pluggable review pane — embedded Neovim CodeDiff, `delta`, or built-in git diff; switch with `--diff`
- Live CI — checks with per-run durations, refreshed automatically while pending
- PR export — `live-pr pr publish` creates or updates the GitHub PR with the timeline as its body

## Install

With Homebrew (macOS):

```sh
brew install shonenm/live-pr/live-pr
```

Or download a platform archive from [GitHub Releases](https://github.com/shonenm/live-pr/releases):

```sh
version=v0.5.1
asset=live-pr_0.5.1_$(uname -s)_$(uname -m).tar.gz
tmp=$(mktemp -d)
curl -fL "https://github.com/shonenm/live-pr/releases/download/$version/$asset" -o "$tmp/$asset"
tar -xzf "$tmp/$asset" -C "$tmp"
install -Dm755 "$tmp/live-pr" "$HOME/.local/bin/live-pr"
```

Or with Go:

```sh
go install github.com/shonenm/live-pr@latest
```

Requirements: Git. An authenticated GitHub CLI (`gh`) for GitHub browsing,
commenting, reviewing, and publishing. Neovim CodeDiff and `delta` are
optional; raw git diff is built in.

## Quick start

```sh
live-pr demo             # disposable demo, no GitHub resources touched
live-pr                  # open the TUI for the current repository and branch
live-pr --diff=delta     # pick the review pane: git, delta, codediff, or a command
live-pr pr publish       # push and create/update the GitHub PR from the timeline
```

Press `?` in the TUI for keybindings, or see [docs/keybindings.md](docs/keybindings.md).

## Configuration

`~/.config/live-pr/config.toml`, overridden per-repo by `.live-pr.toml`:

```toml
theme = "primer-dark"        # primer-dark (default) | primer-light | nord | catppuccin-mocha
summarize_command = ""       # summary backend: transcript on stdin, summary on stdout ("" = claude -p)

[diff]
command = 'nvim -c "CodeDiff --inline $LIVE_PR_RANGE"'  # embedded reviewer ("" = built-in git diff)
display = "delta --color-only"                          # static diff filter
split_ratio = 20                                        # conversation : review width (detail screen)
min_pane_width = 60

[list]
split_ratio = 50             # list : preview width on the PR list screen (default 45)
```

See [docs/diff-tool-integration.md](docs/diff-tool-integration.md) for reviewer details.

## Agent Skill

Teach coding agents when to record decisions:

```sh
gh skill install shonenm/live-pr live-pr --agent claude-code --scope user
```

The binary carries the matching skill version (`live-pr skill path`). A
Claude Code Stop hook (`live-pr init --hooks`) can summarize sessions into
the timeline automatically.

## More

- [docs/keybindings.md](docs/keybindings.md) — full key reference
- [docs/development.md](docs/development.md) — CLI reference, building, debugging
- [docs/prior-art.md](docs/prior-art.md) — how live-pr relates to neighboring tools

## License

[MIT](LICENSE)
