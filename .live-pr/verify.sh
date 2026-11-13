#!/bin/sh
# live-pr local CI verifier. Usage: verify.sh <base-rev>
#
# Writes `<job>=attested` to $GITHUB_OUTPUT for every job a trusted signer ran
# against exactly the checked-out tree. Any mismatch attests nothing, so the
# workflow's jobs simply run as usual.
#
# Trusted keys come from .live-pr/allowed_signers at <base-rev>, never from
# the change under test. Each line is: live-pr <public key>
set -eu

base=$1
none() {
	echo "live-pr: $1; running jobs remotely"
	exit 0
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

git cat-file -e "$base^{commit}" 2>/dev/null || git fetch -q --depth=1 origin "$base" 2>/dev/null || none "base $base is unavailable"
git show "$base:.live-pr/allowed_signers" >"$tmp/signers" 2>/dev/null || none "no .live-pr/allowed_signers at $base"
git fetch -q origin '+refs/notes/live-pr:refs/notes/live-pr' 2>/dev/null || true

tree=$(git rev-parse 'HEAD^{tree}')
note=$(git notes --ref=refs/notes/live-pr show "$tree" 2>/dev/null) || {
	# A pull request checks out a test merge into its base, so an attested
	# branch that is behind base yields a tree nobody ran the checks against.
	head=$(git rev-parse -q --verify 'HEAD^2^{tree}' 2>/dev/null) &&
		git notes --ref=refs/notes/live-pr show "$head" >/dev/null 2>&1 &&
		none "the attestation covers the branch, not its merge into $base (the branch is behind base)"
	none "no attestation for tree $tree"
}
printf '%s\n' "$note" | sed -n 1p >"$tmp/payload"
printf '%s\n' "$note" | sed 1d >"$tmp/sig"

ssh-keygen -Y verify -f "$tmp/signers" -I live-pr -n live-pr -s "$tmp/sig" <"$tmp/payload" >/dev/null 2>&1 || none "attestation signature is not trusted"
# A note is only a ref: it can be copied onto another tree, so the signed
# payload must name this tree itself.
[ "$(jq -r .tree "$tmp/payload")" = "$tree" ] || none "attestation is for another tree"

jq -r '.jobs[]' "$tmp/payload" | while read -r job; do
	echo "$job=attested" >>"${GITHUB_OUTPUT:-/dev/stdout}"
	echo "live-pr: $job attested for tree $tree"
done
