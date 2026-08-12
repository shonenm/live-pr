---
name: live-pr
description: Maintains a local living pull request with a final PR-template summary and a sparse decision timeline. Use while implementing a pull request, when a material design decision changes, when significant review feedback changes scope or behavior, or when preparing/publishing the final PR with live-pr.
license: MIT
compatibility: Requires the live-pr and git commands in a Git repository.
metadata:
  author: shonenm
---

# live-pr

Use `live-pr` as shared review context between the user and coding agents. The TUI is the human surface: users press `a` to add, `e` to edit the selected final summary/comment, `d` to delete a selected local comment, and `Ctrl+S` to save. Agents should use the non-interactive CLI commands below rather than trying to drive the TUI.

Keep two distinct artifacts:

1. **Final summary** — the eventual pull-request description, following the repository template. It describes the implemented result, not the initial plan.
2. **Decision timeline** — a sparse chronological record of context a reviewer cannot recover from the final diff alone.

## Start or resume

```sh
live-pr status --json
live-pr comment list --json
live-pr summary show
```

Run `live-pr init` only when the branch has no local PR state. It seeds the final summary from the repository's default pull-request template when one exists.

Do not rewrite the final summary at the start from an implementation plan. Finalize it after the implementation and validation results are known.

## When to add a comment

Add at most one concise comment when the work produces one of these:

- a material design decision, with a non-obvious tradeoff or rejected alternative;
- a pivot that supersedes an earlier direction;
- a consequential constraint that shapes the implementation or review;
- user or reviewer feedback that materially changes scope, behavior, compatibility, or risk.

Do **not** record:

- routine implementation progress or every completed step;
- test/lint failures and their ordinary fixes;
- readability cleanup, renames, formatting, or minor refactors;
- file lists, commit summaries, commands run, or information obvious from the diff;
- speculative plans that did not become the implemented result;
- a comment merely because a session or turn ended.

If a reviewer would not need the fact to understand *why the final change has this shape*, do not write it.

## Comment commands

Agent-authored comments must use `--author agent`; human-authored CLI comments default to `user`.

```sh
live-pr comment add "Use append-only edit records" \
  --kind decision \
  --author agent \
  --body "Preserves concurrent writers and legacy timeline history."

live-pr comment add "Replace file rewriting with tombstones" \
  --kind pivot \
  --author agent \
  --body-file /tmp/live-pr-rationale.md

live-pr comment list --json
live-pr comment edit <id> "Updated title" --body-file /tmp/live-pr-rationale.md
live-pr comment delete <id>
```

Kinds:

- `decision`: a selected direction and why it won;
- `pivot`: a previous direction changed and why;
- `note`: a significant constraint or review finding that is not itself a decision.

Prefer a short title plus one or two sentences of rationale and reviewer-visible impact. Edit an existing comment when clarifying the same decision; add a `pivot` when the direction actually changed. Delete only noise or an accidental duplicate—do not erase a real decision transition.

## GitHub review workflow

When reviewing an existing GitHub PR, keep comments in a local draft and submit them together with one verdict. Do not use local decision comments for code review findings.

```sh
live-pr review body --body "General review feedback"
live-pr review add internal/app.go --line 42 --side RIGHT --body "Handle this error."
live-pr review show --json
live-pr review submit --event REQUEST_CHANGES
```

Events are `COMMENT`, `APPROVE`, and `REQUEST_CHANGES`. `RIGHT` addresses new/context lines and `LEFT` addresses deleted lines. Submission verifies that the PR head SHA still matches the reviewed revision; if it changed, review the new revision and recreate the stale draft. Use `live-pr review delete <index>` or `live-pr review clear` only before submission.

## Finalize the summary

After implementation stabilizes:

1. Read the repository PR template, current summary, final diff, and relevant validation results.
2. Replace `# <title>` with one concise PR title; if the template has no title heading, keep that H1 above it.
3. Describe the final behavior and reviewer-relevant changes, not the work plan or chronological implementation diary.
4. Preserve applicable template headings and checklists. Fill them with verified facts; do not invent issue links, tests, or outcomes.
5. Keep detailed decision history in comments instead of duplicating it in the summary.
6. Write the complete Markdown and preview it.

```sh
live-pr summary set --file /tmp/live-pr-final-summary.md
live-pr pr preview
```

Use `live-pr summary edit` when the user wants to edit in `$VISUAL` or `$EDITOR`.

Do not run `live-pr pr publish` unless the user explicitly asks to publish/create/update the GitHub pull request. Before publishing, ensure the summary is final and the preview contains only reviewer-relevant timeline entries.
