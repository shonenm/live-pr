# Local-first review release notes

live-pr now treats the checked-out repository as a live review target before,
during, and after publication to GitHub.

## Highlights

- Review a branch before a pull request exists.
- Keep reviewing local commits plus staged, unstaged, and untracked changes after publication.
- See `LOCAL`, `LIVE`, or `REMOTE` in the status line, with exact ahead/behind/diverged state.
- Browse commits as `Published on PR`, `Local only`, `Remote only`, and `Working tree` sections.
- Detect edits and branch switches without restarting live-pr while preserving the active tab, focus, cursor, and viewport.
- Poll PR head, state, draft state, and CI only while synchronized, with capped retry backoff when GitHub is unavailable.
- Continue using local commits and diffs offline with cached GitHub conversation metadata.

## Diff coverage

Local review includes tracked and untracked text, binary files, symlinks,
renames, deletions, merge conflicts, and nested repository/submodule changes.
Statistics come from local Git rather than GitHub's published snapshot.

## Repository support

The local workflow covers linked worktrees, detached `HEAD`, shallow clones,
fork checkout routing, paths containing spaces or Unicode, and the existing
Linux, macOS, and Windows CI matrix.

## Changed behavior

The pull request head is now a publication boundary, not the end of the local
diff. A local edit or commit immediately changes a synchronized review from
`LIVE` to `LOCAL`. A changed remote head does not replace the diff while it is
being read; the status line reports the update and `r` explicitly fetches and
classifies the new history.

See [Local review modes](local-review-modes.md) for the complete state model and workflow.
