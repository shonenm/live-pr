# Local-first visual demo

The default `live-pr demo` fixture opens a diverged checked-out PR entirely
locally. It contains:

- commits shared by the checkout and published PR;
- one local-only commit;
- one remote-only commit;
- one untracked working-tree file.

The status line therefore shows `LOCAL`, ahead/behind counts, and `diverged`.
Press `c` to see `Published on PR`, `Local only`, `Remote only`, and `Working
tree` in one screen. No GitHub resources are created.

Record the reproducible walkthrough with [VHS](https://github.com/charmbracelet/vhs):

```sh
vhs docs/demo/local-first.tape
```

The tape writes `assets/local-first.gif`. Generated recordings are refreshed
only when intentionally publishing new visual assets.
