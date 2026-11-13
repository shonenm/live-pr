---
name: pr
description: Opens or updates a pull request for live-pr itself, following CONTRIBUTING.md and .github/PULL_REQUEST_TEMPLATE.md. Use when asked to create, open, or update a pull request in this repository.
---

# Pull request

Open a pull request against `main` from the current branch. The rules live in
`CONTRIBUTING.md`; this skill is the order to apply them in.

## 1. Check the branch

```sh
git branch --show-current
git status --short
git fetch -q origin && git log --oneline origin/main..HEAD
gh pr view --json url,state 2>/dev/null
```

- On `main`: create a branch first, named `<type>/<slug>` (`feat/list-split-ratio`,
  `fix/popup-wide-rune-overlay`). Fixes land on `main` first, including
  ones that also apply to 1.x; the maintainer backports them to
  `release/1.x`.
- Uncommitted changes: stage only the paths that belong to this change
  (never `git add -A`), and commit with a Conventional Commits subject:
  `type(scope): subject`, where the scope is the package (`tui`, `github`,
  `localci`). Release notes are generated from these subjects.
- An open pull request for this branch already exists: update it (step 5)
  instead of creating another.
- More than one concern on the branch (a fix and a refactor, a feature and an
  unrelated cleanup): stop and propose splitting it.

## 2. Verify

Run what the change touches, and record the results for the test plan:

| Changed | Run |
| --- | --- |
| Any Go code | `just check` (tests with `-race`, gofmt, golangci-lint, govulncheck, module verification, diff check) |
| Platform-specific code (`*_windows.go`, `*_unix.go`, terminal or process handling) | `GOOS=windows go vet ./...` |
| `internal/localci/verify.sh` | `go run . ci verify-script > .live-pr/verify.sh` (a test fails if the committed copy is stale) |
| Only Markdown | Check that the links you added resolve |

A behavior change needs a test. TUI tests drive `Update` with messages
directly; see `internal/tui/*_test.go`. Fix failures caused by the change;
report unrelated ones in the description instead of fixing them here.

## 3. Write the description

Read `.github/PULL_REQUEST_TEMPLATE.md` and keep its structure: every heading,
the HTML comments that still help a reader, and the whole checklist.

- **Summary**: what changes, why, and what a user will notice. Link the issue
  (`Closes #123`) only if one exists; never invent one.
- **Technical details**: design decisions, rejected alternatives, limitations,
  new dependencies, breaking changes. Link the prior art the change follows.
- **How to verify**: commands and TUI keys a reviewer can follow, with the
  expected result.
- **Screenshots**: keep for visible TUI changes, delete otherwise. Do not
  claim a screenshot you did not take.
- **Test plan**: the commands you ran and their outcome, plus what you could
  not verify and why.
- **Checklist**: check only what was actually done. Leave an item unchecked
  when it does not apply; the final item says that is deliberate.

State facts you verified. Do not describe the work history; write for the
reviewer and for the changelog reader.

Say in the description that the change was AI-assisted (CONTRIBUTING.md asks
for it). Do not add co-author trailers or "Generated with" lines.

## 4. Create

```sh
git push -u origin HEAD
gh pr create --base main --title "<type>(<scope>): <subject>" --body-file <file>
```

Write the body to a temporary file (`mktemp`) rather than passing it inline.
When live-pr is installed and the branch has a living summary, `live-pr pr
preview` and `live-pr pr publish` do the same from the summary instead.

## 5. Update an existing pull request

```sh
gh pr edit <number> --title "<title>" --body-file <file>
```

Rewrite the description to match the current diff; do not append a changelog
of revisions. Return the pull request URL when done.
