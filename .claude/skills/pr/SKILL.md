---
name: pr
description: Creates or updates a pull request for live-pr itself, or writes a change summary without opening one. Use when asked to create, open, or update a pull request in this repository, or to summarize the changes on a branch.
---

# Pull request

Use the narrowest mode that was asked for: a pull request from the current
branch, an update to an existing one, or a change summary only. Never include
changes from other worktrees or unrelated files; never `git add -A` or
`git commit -a`.

## Prepare

1. Resolve the repository, the branch, an existing pull request
   (`gh pr view --json number,url,state,isDraft`), and the base (`main`;
   fixes that also apply to 1.x still target `main`, and the maintainer
   backports them to `release/1.x`). Read the full diff
   (`git diff origin/main...HEAD`), the relevant callers, and the
   verification results.
2. Summary only: if the branch has no commits ahead of `origin/main`, use the
   staged diff, and stop if it is empty. Return `# <type>(<scope>): <subject>`
   followed by `### Background` and `### Changes`, written in Japanese. Explain how the changes
   relate instead of listing every path. Do not create an issue or a pull
   request.
3. Resolve the issue this implements from the request or the branch, and
   check it with `gh issue view`. Never invent one. Use `Closes #N` for an
   issue the change completes, and `Refs #N` otherwise.
4. On `main`, create a branch named `<type>/<slug>` first. Commit only the
   paths that belong to the change, with a Conventional Commits subject.
5. Pick the type from the change, not from the paths:

   | Type | Template | Label |
   | --- | --- | --- |
   | `feat` | `.github/PULL_REQUEST_TEMPLATE/feat.md` | `enhancement` |
   | `fix` | `.github/PULL_REQUEST_TEMPLATE/fix.md` | `bug` |
   | `perf`, `refactor` | `.github/PULL_REQUEST_TEMPLATE/refactor.md` | — |
   | `test` | `.github/PULL_REQUEST_TEMPLATE/test.md` | — |
   | `docs` | `.github/PULL_REQUEST_TEMPLATE/docs.md` | `documentation` |
   | `chore`, `ci` | `.github/PULL_REQUEST_TEMPLATE/docs.md` | — |

   The title is `<type>(<scope>): <subject>`, with the package as the
   optional scope. Release notes group `feat`, `fix`, and `perf` and leave
   out `docs`, `test`, `chore`, and `ci`, so the type is for the changelog
   reader. A change that requires existing users to change something (a
   removed or changed CLI flag, config key, or key binding, or on-disk state
   the previous version can no longer read) adds `!` after the type.

## Verify

Run what the change touches before writing the description:

| Changed | Run |
| --- | --- |
| Any Go code | `just check` and `GOOS=windows go vet ./...` |
| `internal/tui/` | Also run it in a real terminal against a repository with a pull request, in a narrow and a short window |
| `internal/localci/verify.sh` | `go run . ci verify-script > .live-pr/verify.sh` |
| `.goreleaser.yaml`, `.github/workflows/release.yml`, `scripts/release` | `goreleaser release --snapshot --clean` |
| Only Markdown | Check that every link you added resolves |

A behavior change needs a test; TUI tests drive `Update` with messages
directly (`internal/tui/*_test.go`). Collect performance baselines before
implementing a performance claim; if they are missing at pull request time,
record the measurement as not verified rather than reconstructing it.

## Body

- Write the body in Japanese. Keep the headings and the checklist items in
  English, exactly as the template has them.
- Start from the template for the type and keep every heading and the
  checklist. The HTML comment under each heading is the guidance for that
  section; follow it, then delete the comment. Templates are the single
  definition of the sections; do not keep another copy of their wording here.
- Write for a reviewer who has seen neither the code nor this session, from
  the whole to the details: before any detail, the reader must know what
  changes and in which situation. Name each code element uniquely on first
  mention (path and symbol, command, config key, key binding) with its role,
  never a bare generic noun such as "the handler".
- Classify the prior state before explaining it: an observed defect, a
  planned increment not built yet, a dependency or contract change not yet
  followed, or a new requirement. Call it a problem only for an observed
  defect, and say which change or broken assumption introduced it.
- Distinguish what tests will keep guaranteeing (new, removed or weakened,
  and unguarded) from what you ran once. If a `feat` or `refactor` changes no
  tests, state the unguarded behavior. Write "not verified" with the reason
  for anything you did not run.
- Open every source you cite and check its version and location. Trace each
  important claim from the diff to a source or an observed result.
- Use at most one Mermaid diagram, and only when a flow across components or
  a state machine would otherwise have to be reconstructed by the reader.
- Leave Screenshots empty unless you have media you actually captured. A
  unit test is not a screen check.
- Provenance: one line each for who implemented the change (a person, or an
  agent and the tool it ran in) and who wrote this description. Do not
  infer people from logins or hostnames, and do not include session IDs.
  This is how CONTRIBUTING.md's request to disclose AI assistance is met; do
  not add co-author trailers or "Generated with" lines to commits or the
  description.
- Roadmap stage names (A1, A2) go only in Related requests and plans, with
  what the stage is and where it is described. The title and the rationale
  must make sense without them.
- Leave every checklist item unchecked. The checklist belongs to a human.
- No `## Summary` or `## Test plan` headings, and no bold text.

Write the body to a temporary file (`mktemp`), then check it before posting:
every heading of the template is present, no `- [x]`, no `**`, and the title
matches `^(feat|fix|perf|refactor|docs|test|chore|ci)(\([a-z0-9-]+\))?!?: `.

## Create or update

- Create: push with `git push -u origin HEAD`, then

  ```sh
  gh pr create --draft --base main --title "<title>" --body-file "$body" --assignee @me [--label <label>]
  ```

  Agent-authored pull requests are always drafts. Marking one ready for
  review, and checking the checklist, belong to a human; never mark it ready
  yourself.
- When live-pr is installed and the branch has a living summary,
  `live-pr pr preview` and `live-pr pr publish` create it from that summary
  instead; the summary must still follow the template.
- Update: `gh pr edit <number> --title "<title>" --body-file "$body"`. Keep
  Implemented by as it was; record yourself only under Description written
  by. Once a human has checked any checklist item, change nothing but what
  was asked. If `gh pr edit` fails on a deprecated Projects warning, use
  `gh api -X PATCH "repos/{owner}/{repo}/pulls/<number>" -F "body=@$body"`.

Report the issue, the branch, the pull request URL, the verification results,
and what is left for a human (the checklist and ready for review).
