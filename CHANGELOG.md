# Changelog

All notable changes to this provider are documented here. This project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Published versions are immutable. A defect is corrected with a new patch
release, never by replacing a published tag or asset.

## Unreleased

## 0.1.0-rc.2 - 2026-09-03

Re-cut of the first candidate. `0.1.0-rc.1` built and signed correctly but the
Terraform Registry refused to ingest it: its `SHA256SUMS` named the SBOM files,
and the Registry reads that file as the manifest of the release and requires
every entry to be a file it ingests. No version of `0.1.0-rc.1` was ever
published to the Registry, so nothing could have installed it.

### Fixed

- SBOMs are no longer listed in `SHA256SUMS`. They are still published with
  every release, built after the archives rather than as part of them, so a
  consumer who wants one still gets it.

## 0.1.0-rc.1 - 2026-09-03

First public release candidate, published so the install path can be exercised
end to end from the Terraform Registry. Treat it as a candidate: the interface
is expected to be stable, but nothing has yet been installed by anyone outside
the project. `0.1.0` follows once that has been.

Releases from this version onward are signed by a key held in Yotta Keys and
used through a short-lived credential minted per release. No private key exists
on the build runner, and none is stored in this repository or its secrets.
Verifying a release is described in [VERIFY_ARTIFACTS.md](VERIFY_ARTIFACTS.md).

### Added

- Initial provider with five resources, each supporting create, read, update,
  delete, and import by UUID:
  - `yottabot_agent`
  - `yottabot_workflow`
  - `yottabot_context_provider`
  - `yottabot_mcp_gateway`
  - `yottabot_mcp_tool`
- Two authentication paths: a static bearer token, and service-account OAuth
  client credentials using an RFC 7523 Ed25519 client assertion with in-memory
  token caching and refresh before expiry.
- Environment fallback for every provider setting. `YOTTABOT_*` is canonical;
  `YOTTA_*` is accepted as a compatibility alias, and the canonical name wins
  when both are set.

### Notes for first-time users

These are deliberate behaviors, documented because each one would otherwise
look like a bug:

- `status` is adopted from the server when omitted from config, so publishing
  an agent outside Terraform does not show up as drift.
- Removing `prompt_id` forces replacement — the update route cannot clear it.
- `trigger = "schedule"` is refused. The API normalizes it to `cron`, so
  accepting it would produce a plan that never converges.
- `yottabot_mcp_tool` names its vendor field `vendor`. The wire field is
  `provider`, which Terraform reserves as a meta-argument.
- Destroying a `yottabot_context_provider` retires it rather than deleting it.
  Recreating with the same `type` and `external_id` is refused; import the
  existing row instead.
