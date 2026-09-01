# Security Policy

## Supported versions

live-pr is alpha software. Only the latest release receives fixes; there are no
backports to older tags. Check your version with `live-pr --version` and
upgrade before reporting.

## Reporting a vulnerability

Report privately through GitHub's
[private vulnerability reporting](https://github.com/shonenm/live-pr/security/advisories/new)
— do not open a public issue.

Please include the version, your OS, and the steps to reproduce. Expect an
acknowledgement within a week; this is a single-maintainer project, so a fix
may take longer than that. You will be credited in the advisory unless you ask
otherwise.

## What live-pr does on your machine

Knowing where the trust boundaries sit helps when judging whether something is
a vulnerability.

**live-pr runs commands you configure.** `diff.command`, `diff.commit_command`,
`ci.command`, and `summarize_command` are executed through a shell. This is the point of
those settings — they launch your reviewer and your summarizer — but it means
configuration is code.

Configuration is read from `~/.config/live-pr/config.toml` and, if present,
`.live-pr.toml` **inside the repository you are working in**. A repository you
clone can therefore ship a `.live-pr.toml` that runs a command on your machine
the first time you start live-pr in it. This is the same exposure as Vim's
`exrc` or a direnv `.envrc`: review the per-repo configuration of repositories
you do not trust before running live-pr there. Reports of this behavior on its
own are not treated as vulnerabilities; ways to bypass it, or to inject
commands where a shell is not expected, are.

**live-pr shells out to `git` and `gh`.** It never handles your GitHub
credentials directly — authentication belongs to `gh` — and it runs both tools
with argument arrays rather than a shell, so repository data such as branch and
file names is not interpreted as commands.

The optional Woodpecker provider similarly reuses `woodpecker-cli` authentication.
On headless hosts, `ci.server`, `ci.cli_command`, and `ci.token_command` may be
set in the global configuration only; repository configuration cannot override
them. The token
command is executed without a shell, and its one-line output is passed only to
the child CLI as `WOODPECKER_TOKEN`, without logging or persistence.

**live-pr writes to your XDG state directory.** Timelines, review drafts,
outbox entries, and cached GitHub data (including private repository content)
are stored with owner-only permissions under `~/.local/state/live-pr` or the
platform equivalent.

## Scope

In scope: command injection through data live-pr does not control (repository
contents, GitHub API responses, file names), leaking cached private data to
other users on a shared machine, tampering with release artifacts, and any
network traffic outside `gh` and `git`.

Out of scope: configuration executing configured commands (documented above),
issues that require an attacker who already has your user account, and
vulnerabilities in `gh`, `git`, or your configured reviewer — report those
upstream.
