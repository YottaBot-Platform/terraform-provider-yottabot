# Release process

Releases are immutable SemVer tags. **A published tag or release asset is never
replaced.** Correct a defect with a new patch release, and if the bad version is
dangerous, mark it deprecated and point release notes at the fix.

## Versioning

- Stable: `vX.Y.Z`. Release candidates: `vX.Y.Z-rc.N`.
- **Breaking changes wait for a major release.** Breaking means a change to
  provider configuration, resource schema, import ID format, or state shape —
  anything that can turn a working configuration into a failing plan.
- Deprecations stay for at least one stable minor and ship with a replacement or
  migration path, except where an urgent security fix makes that impossible.
- While the provider is pre-1.0, breaking changes may land in a minor. This is
  stated in the README so practitioners pin exact versions.

## Cadence

Monthly when there are user-visible changes, plus out-of-band releases for
security and critical fixes. **Do not publish an empty release to satisfy the
calendar.**

## Cutting a release

1. **Open a release PR** containing the version bump, the assembled
   `CHANGELOG.md` entry, regenerated docs, and any compatibility-matrix update.
   Nothing else — a release PR that also changes behavior cannot be reviewed as
   a release.
2. **Confirm the gates pass on that commit**: tests, vet, gofmt, tidy, the leak
   scan, docs reproducibility, and example formatting.
3. **Compare the schema against the last stable release.** Removals, type
   changes, new ForceNew fields, and import-ID changes are breaking and must not
   reach a minor or patch. This is the check that catches an accidental
   breaking change before it is immutable.
4. **Merge, then tag the reviewed commit** — annotated, `vX.Y.Z`. Never tag a
   commit that was not reviewed as a release.
5. **Approve the protected `release` environment.** The workflow runs its own
   preflight (tests, docs current, changelog names this version, clean tree)
   before it builds anything.
6. **Verify the artifacts** per [VERIFY_ARTIFACTS.md](VERIFY_ARTIFACTS.md),
   from a clean machine, before announcing.
7. **Publish to the Terraform Registry** if this is the first release; later
   releases are picked up automatically from the tag.
8. **Run the canary**: in a clean directory, `terraform init` against the
   published version, then apply, import, plan (expect no diff), and destroy the
   acceptance stack against a non-production estate.

## Release candidates

RCs publish to the prerelease channel. `prerelease: auto` in the GoReleaser
config marks any tag with a prerelease suffix, which keeps the Registry from
presenting an RC as the recommended version.

Promote by tagging the **same commit** with the stable version. Do not rebuild
from a different tree.

## What can go wrong, and what to do

**A release published with a bug.** Leave it available — someone's lockfile
pins it. Ship a patch, and point the issue and the release notes at the fixed
version.

**A release published with a leaked secret or an internal reference.** Rotate
the secret first; assume the artifact was downloaded. Removal is an exception
reserved for security and legal response, not embarrassment.

**Docs are wrong on the Registry.** Registry documentation is tied to the tag,
so this needs a new version. That is why docs reproducibility is a merge gate
rather than a release step.

**The signing key is compromised.** Revoke it with the Registry, generate a new
one, and re-release. Previously published checksums stay signed by the old key;
say so in the advisory rather than silently re-signing.

## Signing

Releases sign the `SHA256SUMS` file with a detached GPG signature. The Registry
requires it — an unsigned release uploads fine and then fails to publish.

The private key and its passphrase live as secrets on the protected `release`
environment (`GPG_PRIVATE_KEY`, `GPG_PASSPHRASE`), and the public key is
registered with the Registry publishing account. Fork workflows never see them.
