# Contributing

Thanks for taking a look. live-pr is an alpha-stage project maintained by one
person, so the fastest way to land a change is to open an issue first and agree
on the approach before writing code.

## Before you start

- **Bugs**: open an issue with `live-pr --version`, your OS, your terminal
  emulator, and the steps to reproduce. Terminal UI bugs are hard to reproduce
  without that.
- **Features**: open an issue describing the workflow you want, not the
  implementation. The TUI has a deliberate shape and a feature that fits it is
  easier to accept than one that fits a patch.
- **Small fixes** (typos, obvious bugs, doc corrections): just send the pull
  request.

Because there is a single maintainer, review can take a few days. A pull
request that arrives without a prior issue may be declined on scope grounds
even when the code is fine — please ask first for anything larger than a fix.

## Development

```sh
go build -o live-pr .   # build
just check              # tests, race checks, gofmt, golangci-lint, govulncheck, module verification, diff check
```

`just check` is what CI runs. Run it before pushing.

Requirements: Go (see `go.mod`), Git, and an authenticated `gh` for anything
that talks to GitHub. The linter and the vulnerability scanner run through `go
run` at the versions CI pins, so there is nothing else to install.
`LIVE_PR_DEBUG_TIMING=1` prints startup, Git, GitHub, and render timings to
stderr.

See [docs/development.md](docs/development.md) for the CLI reference,
[docs/diff-tool-integration.md](docs/diff-tool-integration.md) for the review
pane contract, and [docs/keybindings.md](docs/keybindings.md) for the key map.

Dogfooding is welcome: live-pr reviews its own pull requests, so running it
against your own branch is a good way to find rough edges.

## Pull requests

Branch from `main` using `<type>/<slug>`, matching the commit type:

```
feat/list-split-ratio    fix/popup-wide-rune-overlay    docs/contributing
```

Commits follow [Conventional Commits](https://www.conventionalcommits.org/)
with an optional scope naming the package:

```
feat(tui): color the PR number with its state
fix(github): reject events too large for the timeline reader
refactor(tui): migrate to bubbletea v2 and bubbles v2
```

Release notes are generated from these subjects, so write them for the person
reading the changelog.

What a reviewable pull request looks like here:

- **One concern per pull request.** Correctness fixes, refactors, and features
  are reviewed with different eyes; mixing them makes both harder to judge.
- **Behavior changes come with a test.** The suite is the specification for a
  TUI that is otherwise awkward to verify by hand. Tests drive `Update` with
  messages directly — see `internal/tui/*_test.go` for the pattern.
- **Refactors keep the tests unchanged** apart from mechanical relocation.
  Weakening an assertion to make a refactor pass hides the regression it was
  there to catch.
- **Comments explain constraints, not narration.** Say why the non-obvious
  thing is necessary; the code already says what it does.

## AI-assisted contributions

live-pr is largely written by AI agents — that is the workflow it exists to
support, and the repository ships an Agent Skill for it. AI-assisted pull
requests are welcome on one condition: **you have read and verified the diff
yourself, and you can answer questions about why it works.**

Please say in the pull request that it was AI-assisted. It is not held against
you; it tells the reviewer where to look. What gets declined is not the tool
but the pattern — a generated diff nobody has read, tests that assert whatever
the implementation happens to do, or a description that does not match the
change.

## Security

Do not open a public issue for a vulnerability. See [SECURITY.md](SECURITY.md)
for how to report one privately.

## License

Contributions are accepted under the [MIT License](LICENSE).
