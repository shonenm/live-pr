<!-- Thanks for contributing! Fill in what applies and delete what does not.
     See CONTRIBUTING.md for what a reviewable pull request looks like here. -->

## Summary

<!-- What changes and why, and what a user will notice. Link the issue this
     implements, if there is one (`Closes #123`). -->

## Technical details

<!-- Design decisions, alternatives you rejected, limitations, new
     dependencies, and anything that breaks compatibility (CLI flags, config
     keys, keybindings, on-disk state). Prior art you followed is worth a link. -->

## How to verify

<!-- Steps a reviewer can follow: the commands to run, the keys to press in
     the TUI, and what they should see. -->

## Screenshots

<!-- For visible TUI changes: a screenshot or a short recording. Delete this
     section otherwise. -->

## Test plan

<!-- How you verified this. `just check` is the baseline; name the tests you
     added for a behavior change. -->

- [ ] `just check`

---

## Checklist

### Pull request

- [ ] The title follows Conventional Commits (`type(scope): subject`) and reads well in a changelog
- [ ] It addresses one concern (a fix, a refactor, or a feature, not a mix)
- [ ] The related issue is linked, or this is a small fix that does not need one
- [ ] AI assistance, if any, is disclosed, and I have read and verified the diff myself

### Quality

- [ ] A behavior change comes with a test (`internal/tui/*_test.go` drives `Update` directly)
- [ ] A refactor leaves the tests unchanged apart from mechanical relocation
- [ ] `GOOS=windows go vet ./...` passes when the change touches platform-specific code
- [ ] User-facing docs are updated (`README.md`, `docs/`, `docs/keybindings.md`)

### Compatibility

- [ ] Nothing that GitHub shows on a pull request is dropped from live-pr's view
- [ ] Breaking changes to CLI flags, config keys, keybindings, or on-disk state are described above
- [ ] If the fix also applies to `1.x`, the description says so, so it can be backported to `release/1.x`

### Local CI

- [ ] Changes to `.live-pr.toml`, `.live-pr/`, or the workflow's `attest` gate are called out above (they change what the remote trusts)

### Final check

- [ ] Every item above is checked, or deliberately left unchecked because it does not apply
