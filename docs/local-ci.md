# Local CI

live-pr can run your repository's check commands in your checkout, sign the
result, and let the remote workflow skip the jobs that were already proven
against the same tree. The remote stays authoritative: without a valid
attestation every job runs as usual.

## How it works

1. `R` in the TUI (or `live-pr ci run`) runs the `[ci.local.checks]` commands
   in place, reusing your toolchains and caches. No clone, no container.
2. If the checkout was clean before and after the run, the passing checks are
   signed with your SSH key and stored as a git note on `HEAD`'s tree object
   (`refs/notes/live-pr`), which is then pushed to `origin`.
3. A verification job at the start of the remote workflow checks the note and
   exposes `<check>=attested` outputs. Attested jobs are skipped with `if:`.

The attestation names a tree, not a commit. It survives a squash merge or
rebase that produces the same tree and is void as soon as anything differs:
the base moved, `.live-pr.toml` or a script changed, or a file was edited.

## What runs locally

A check is a name and a shell command. live-pr does not read the workflow:
point the check at the repository's own entry point (`just check`, `bin/ci`,
`make test`) and have the remote job call the same command. That shared
script is what makes a local pass comparable to a remote one.

```toml
# .live-pr.toml
[ci.local.checks]
test = "just check"
```

```yaml
# .github/workflows/ci.yml
  test:
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
      - run: just check
```

Pin toolchains in the repository (`go.mod`'s `toolchain`, mise, a lockfile) so
both sides resolve the same versions; a newer local toolchain is a real
difference, not a detail. A workflow without such a script can use a runner
such as [act](https://github.com/nektos/act) as the command instead.

```toml
# ~/.config/live-pr/config.toml — never read from a repository
[ci]
signing_key = "~/.ssh/id_ed25519.pub"   # private key, or a public key held by ssh-agent
```

Without `signing_key`, or with uncommitted changes, the run still reports
results but attests nothing.

## Remote setup

Commit the verifier and the trusted keys (make sure `.live-pr/` is not in
`.gitignore`; older live-pr versions kept runtime state there):

```sh
mkdir -p .live-pr
live-pr ci verify-script > .live-pr/verify.sh
echo "live-pr $(cat ~/.ssh/id_ed25519.pub)" >> .live-pr/allowed_signers
```

Then gate each job on the verifier, using the check name as the output:

```yaml
jobs:
  attest:
    runs-on: ubuntu-latest
    outputs:
      test: ${{ steps.verify.outputs.test }}
    steps:
      - uses: actions/checkout@v7
      - id: verify
        run: sh .live-pr/verify.sh "${{ github.event.pull_request.base.sha || github.event.before }}"

  test:
    needs: attest
    if: >-
      !cancelled() && (github.event_name != 'pull_request'
      || needs.attest.result != 'success'
      || needs.attest.outputs.test != 'attested')
    # unchanged
```

The job is skipped only on a positive attestation. `!cancelled()` and the
`needs.attest.result` clause make it run when the verifier itself fails:
otherwise `needs` would skip it, and GitHub reports a skipped job as
successful. The `github.event_name` clause keeps a full remote run on pushes
to the default branch. Drop it to skip there as well.

On a pull request, GitHub checks out a test merge of the branch into its base.
That merge has the attested tree only while the branch is up to date with the
base; otherwise the job runs remotely and the verifier says why.

`allowed_signers` is read from the base revision, so the pull request that
first adds it — or adds a new signer — is itself verified remotely.

## Blocking merges

live-pr does not stop a merge itself. To refuse a change that has neither an
attestation nor a passing remote run, make each gated job a required status
check: in the repository settings, add a branch ruleset (or branch protection
rule) for the default branch with "Require status checks to pass", and add the
job names (`test` above).

That is enough because GitHub reports a job skipped by `if:` as successful. An
attested job is skipped and passes; an unattested one runs remotely, and the
pull request cannot merge until it passes. The `attest` job does not need to
be required: when it fails, the gated jobs run instead of being skipped.

Whether the branch must be up to date with the base before merging is the
ruleset's choice ("Require branches to be up to date before merging"). With
it, the attested tree is always the one GitHub tests; without it, a branch
behind its base simply runs its jobs remotely.

## Trust model

The gate is "the agent cannot open it by itself", not "tamper-proof":

- **Forged or replayed results** fail verification: the signature covers the
  tree hash, and a note copied onto another tree is rejected.
- **A signer added by the change under test** is ignored: keys come from the
  base revision.
- **The signing key** must be out of the agent's reach. Use a key that needs
  user presence (Secure Enclave, a hardware key, or an ssh-agent that prompts)
  rather than an unencrypted file the agent can read.
- **Ignored files and local toolchains are trusted.** That is the cost of not
  rebuilding the environment. Keep a full remote run on pushes to the default
  branch as the backstop.
- **Weakening the checks themselves** (editing tests, the script, or
  `[ci.local.checks]`) is not a signature problem. Neither is a remote job that
  does more than the local command: skipping it skips the difference. Those changes are in the diff;
  review them.
