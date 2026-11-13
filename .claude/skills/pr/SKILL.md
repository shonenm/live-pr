---
name: pr
description: Creates or updates a pull request for live-pr itself, from branch checks and verification to a description that follows .github/PULL_REQUEST_TEMPLATE.md. Use when asked to create, open, or update a pull request in this repository.
---

# Pull request

Create a pull request from the current branch to `main`. The rules come from
`CONTRIBUTING.md`; this skill is the order to apply them in.

## Prerequisites

- `gh auth status` succeeds.
- The branch is not `main`. If it is, create a branch first (step 2).

## 1. Check the state

```sh
git branch --show-current
git status --short
git fetch -q origin
git log --oneline origin/main..HEAD
git diff --stat origin/main...HEAD
gh pr view --json number,url,state 2>/dev/null
```

- An open pull request for this branch already exists: update it (step 6)
  instead of creating another.
- The branch mixes concerns (a fix and a refactor, a feature and an unrelated
  cleanup): stop and propose splitting it. Each concern is reviewed with
  different eyes.
- The branch is behind `origin/main`: merge or rebase it before verifying, so
  the checks run against what will be merged.

## 2. Branch and commit

Branch names are `<type>/<slug>` (`feat/list-split-ratio`,
`fix/popup-wide-rune-overlay`). On `main`, infer the type from the changes and
create the branch.

Take the commit type from the branch name:

| Branch | Type |
| --- | --- |
| `feat/*` | `feat` |
| `fix/*` | `fix` |
| `perf/*` | `perf` |
| `refactor/*` | `refactor` |
| `docs/*` | `docs` |
| `test/*` | `test` |
| `ci/*` | `ci` |
| `chore/*` | `chore` |

Commit subjects follow Conventional Commits, with the package as the optional
scope: `fix(tui): keep the cursor on the selected row`. Release notes are
grouped by `feat`, `fix`, and `perf`, and `docs`, `test`, `chore`, and `ci`
are left out, so pick the type for the changelog reader. Mark a breaking
change with `!` (`feat(config)!: ...`).

Stage only the paths that belong to this change; never `git add -A` or
`git commit -a`.

Fixes land on `main` first, including ones that also apply to 1.x; the
maintainer backports them to `release/1.x`.

## 3. Verify

Run the checks for what changed, and keep the results for the description:

| Changed | Run |
| --- | --- |
| Any Go code | `just check` (tests with `-race`, gofmt, golangci-lint, govulncheck, module verification, diff check) |
| Any Go code | `GOOS=windows go vet ./...` |
| `internal/tui/` | Also try it in a real terminal: `go run .` in a repository with a pull request, in a narrow and a short window |
| `internal/localci/verify.sh` | `go run . ci verify-script > .live-pr/verify.sh` (a test fails if the committed copy is stale) |
| `.goreleaser.yaml`, `release.yml`, `scripts/release` | `goreleaser release --snapshot --clean` |
| Only Markdown | Check that every link you added resolves |

A behavior change needs a test. TUI tests drive `Update` with messages
directly; see `internal/tui/*_test.go`. Fix failures caused by the change.
Report unrelated failures in the description instead of fixing them here.

## 4. Write the description

Read `.github/PULL_REQUEST_TEMPLATE.md` first and follow its structure
exactly: keep every heading, the HTML comments, and every checklist item.
Delete only the conditional checklist sections (`#### If ... changed`) that do
not apply, and the Screenshots section when nothing visible changed.

Write the body to a temporary file (`mktemp`).

### Summary

- What changed and why, and what a user will notice.
- `Closes #123` for the issue this implements. Never invent an issue number.

### Technical details

- Design decisions and the alternatives you rejected.
- Limitations, the reason for any new dependency, benchmarks.
- Breaking changes: CLI flags, config keys, keybindings, on-disk state.
- The prior art the change follows, with a link.

Describe the changes as a flow, not a file list: group related changes, name
files and functions in `code`, and explain how they connect. Leave out files
that changed only mechanically.

### How to verify

Steps a reviewer can follow:

1. Check out the branch and build it (`go build -o live-pr .`).
2. Prepare the situation the change is about (a branch, a pull request, a
   config entry).
3. Run the command or press the keys, and say what should appear.

### Screenshots

Attach a screenshot or recording for visible TUI changes. Never claim one you
did not take; if you could not capture it, say so.

### Checklist

Check an item only when it was actually done:

| Item | When to check |
| --- | --- |
| Description, verification steps | After writing them |
| Screenshot or recording | When attached; leave unchecked if nothing visible changed |
| Issue linked | When an issue exists and is linked, or the change is a small fix |
| Title format | After checking it against the type table |
| One concern | After step 1 found no mix |
| AI assistance disclosed | When the description says so (always, for agent-written changes) |
| `just check`, `GOOS=windows go vet` | When they ran and passed in step 3 |
| Tests added | When the change adds or updates tests |
| Conditional sections | When the matching command or manual check ran |
| Compatibility | When you confirmed it, not by default |
| Final check | Last, once every other item is decided |

CONTRIBUTING.md asks for AI assistance to be disclosed: add a line such as
`AI-assisted (<tool>).` at the end. Do not add co-author trailers or
"Generated with" lines to commits or the description.

## 5. Create

```sh
git push -u origin HEAD
gh pr create --base main --title "<type>(<scope>): <subject>" --body-file "$body" --assignee @me
```

When live-pr is installed and the branch has a living summary, `live-pr pr
preview` and `live-pr pr publish` create the pull request from that summary
instead.

Return the pull request URL.

## 6. Update an existing pull request

```sh
gh pr edit <number> --title "<title>" --body-file "$body"
```

Rewrite the description to match the current diff; do not append a log of
revisions.

If `gh pr edit` fails (for example on a deprecated Projects warning), update
it through the API:

```sh
gh api -X PATCH "repos/{owner}/{repo}/pulls/<number>" -F "body=@$body"
```
