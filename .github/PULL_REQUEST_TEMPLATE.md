<!-- Thanks for contributing! See CONTRIBUTING.md for what a reviewable pull
     request looks like here. -->

## Summary

<!-- What changed and why, and how it affects the user. Link the issue this
     implements, if there is one (`Closes #123`). -->

## Technical details

<!-- Design decisions and the alternatives you rejected, limitations, the
     reason for any new dependency, benchmarks, and breaking changes. Link the
     prior art the change follows. -->

## How to verify

<!-- Steps to reproduce the issue or try the change: the commands to run, the
     keys to press in the TUI, and what you should see. -->

## Screenshots

<!-- For visible TUI changes, attach a screenshot or a short recording. Delete
     this section otherwise. -->

---

## Checklist

### Pull request

- [ ] The description says what changed and why
- [ ] The verification steps are written down
- [ ] Visible TUI changes come with a screenshot or recording
- [ ] The related issue is linked, or this is a small fix that does not need one
- [ ] The title follows Conventional Commits (`type(scope): subject`) and reads well in the release notes
- [ ] It addresses one concern (a fix, a refactor, or a feature, not a mix)
- [ ] AI assistance, if any, is disclosed, and I have read and verified the diff myself

<!-- Fill in the sections below that apply to your change and delete the
     rest. -->

### Quality

- [ ] `just check` passes
- [ ] Tests for the change are added (a behavior change needs one)
- [ ] A refactor leaves the tests unchanged apart from mechanical relocation
- [ ] `GOOS=windows go vet ./...` passes

#### If `internal/tui/` changed

- [ ] Tests drive `Update` with messages directly (see `internal/tui/*_test.go`)
- [ ] Checked in a real terminal, including a narrow and a short window
- [ ] New or changed keys are reflected in the help and `docs/keybindings.md`

#### If `internal/localci/` changed

- [ ] `.live-pr/verify.sh` is regenerated with `go run . ci verify-script`
- [ ] The trust model in `docs/local-ci.md` still holds, or is updated

#### If `skills/live-pr/SKILL.md` changed

- [ ] Every command the skill mentions exists in this version (the skill ships inside the binary)

#### If the release setup changed (`.goreleaser.yaml`, `.github/workflows/release.yml`, `scripts/release`)

- [ ] `goreleaser release --snapshot --clean` succeeds
- [ ] `docs/releasing.md` is updated

### Compatibility

- [ ] On-disk state and caches written by the previous version are still read, or migrated
- [ ] CLI flags, config keys, and keybindings keep working, or the break is described above
- [ ] Nothing that GitHub shows on a pull request is dropped from live-pr's view
- [ ] If the fix also applies to `1.x`, the description says so, so it can be backported to `release/1.x`
- [ ] Changes to `.live-pr.toml`, `.live-pr/`, or the workflow's `attest` gate are called out above (they change what the remote trusts)

### Final check

- [ ] Every applicable item above is checked; anything unchecked does not apply or is deliberately left out
