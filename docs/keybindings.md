# Keybindings

Press `?` inside the TUI to toggle the full help.

## PR list

| Key | Action |
| --- | --- |
| `j` / `k` | move selection (reaching the last loaded row fetches the next page) |
| `h` / `l`, `[` / `]` | previous / next view (Assigned, Review requested, All, Authored, Needs me, Closed) |
| `V` | manage views (add / edit / delete the view list) |
| `/` | filter (server-side search; `ci:` and `merge:` filter locally), `Enter` to apply |
| `Space` | collapse / expand a PR stack |
| `Enter` | open the selected PR |
| `o` | open the PR on GitHub (copies the URL to the clipboard when no browser is available, e.g. over SSH) |
| `y` | copy the PR URL to the clipboard |
| `s` | change PR status (Close / Reopen / Draft) |
| `c` | checkout the PR branch |
| `x` | close the PR |
| `m` | merge the PR (pick merge commit / squash / rebase in the popup) |
| `r` | refresh |
| `gg` / `G`, `Ctrl+U` / `Ctrl+D` | jump / scroll |
| `q`, `Ctrl+C` | quit |

## PR detail

| Key | Action |
| --- | --- |
| `Tab` | move focus between conversation and review pane |
| `Shift+Tab` | toggle the focused pane full width |
| `l` | focus the review pane |
| `j` / `k` | move selection / scroll the focused pane |
| `c` / `f` / `i` | commits / conflicts / CI checks view (`Esc` back to conversation) |
| `Enter` | on a commit: review that commit |
| `a` | post a conversation comment (GitHub PR) or a local note |
| `A` | add an inline review comment for the selected file |
| `v` | review: pick a verdict (comment / approve / request changes), write the body, submit |
| `e` | edit the selected item — your own GitHub comment, the PR description, or a local comment/summary |
| `d` | delete the selected comment (yours only; confirmation popup) |
| `s` | change PR status |
| `o` | open on GitHub / copy URL |
| `y` | copy the URL of the selected item to the clipboard |
| `p` | publish the PR from the timeline |
| `m` | merge (pick merge commit / squash / rebase in the popup) |
| `C` | checkout the PR branch (`c` stays on the commits view) |
| `b` | back to the PR list |
| `r` | refresh |
| `q`, `Ctrl+C` | quit |

While the embedded reviewer (e.g. Neovim CodeDiff) is focused, all keys
except `Tab`, `Shift+Tab`, and `q` are forwarded to it.

## Mouse

| Action | Result |
| --- | --- |
| Wheel over the PR list | move the selection three rows (same pagination as `j` / `k`) |
| Wheel over a preview / conversation / diff pane | scroll that pane; the pointer decides which one |
| Wheel over the file explorer | move the file selection |
| Click a PR row | select it; clicking the already-selected row opens it |
| Click a view tab in the header | switch to that view |
| Click a conversation or file row | focus that pane and select the row |
| Click the review pane | focus it |
| Click an option in the merge or PR-status popup | move the cursor there; clicking the selected option confirms it |
| Click outside a popup | cancel it (the `Esc` equivalent) |

Only the left button is used. Confirmation popups (checkout, close, delete,
discard) still answer to `y` / `n` only, so no destructive action fires from a
stray click. While the embedded reviewer is focused, mouse events inside its
region are forwarded to it.

## Editor popup

| Key | Action |
| --- | --- |
| `Enter` | newline |
| `Ctrl+S` | send / save |
| `Esc` | cancel |

## Review submit popup (`v`)

| Key | Action |
| --- | --- |
| `j` / `k` | choose verdict |
| `Enter` | write the review body, then `Enter` / `Ctrl+S` submits |
| `Esc` | cancel |
