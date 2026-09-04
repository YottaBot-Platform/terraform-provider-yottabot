# Changelog

All notable changes to this provider are documented here. This project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Published versions are immutable. A defect is corrected with a new patch
release, never by replacing a published tag or asset.

## Unreleased

### Added

- `yottabot_guardrail_policy` — guardrail policies agents reference.
- `yottabot_llm_gateway` — configured routes to upstream inference providers.
- `yottabot_role` — access roles, the join point between groups and policies.
- `yottabot_group` — human groups and the permissions they grant.
- `yottabot_machine_group` — grouping for service accounts and robots.
- `yottabot_policy` — access policies and their statements.

### Notes for first-time users

Two behaviours on these resources are deliberate and would otherwise look like
bugs:

- **Removing an optional attribute clears it.** These APIs preserve on absence,
  so the provider sends an explicit empty value when you delete a line from
  your config. Removing `definition` from a guardrail policy resets it to `{}`
  rather than null, because that column cannot hold null.
- **`upstream_provider` on an LLM gateway forces replacement.** The API has no
  update path for it — a different upstream is a different gateway. It is named
  `upstream_provider` rather than `provider` because Terraform reserves
  `provider` as a meta-argument, and `vendor` on that resource already means
  something else: the gateway's own owner, not the service it calls.

`yottabot_policy`'s `statements` is an **ordered list**, not a set: the server
assigns each statement's evaluation position from its order, and a `deny`
short-circuits an `allow`, so reordering the list changes what the policy means.
An empty list is meaningful — it removes every statement.

`kind` is not settable on a policy. The API accepts `system`, but a system policy
refuses both update and delete, so a settable `kind` would let you create a
resource Terraform could never change or destroy.

**`yottabot_guardrail_policy.definition` is optional *and* computed**, so
removing it from your config does **not** clear it — Terraform keeps the last
applied value. Set it explicitly to `jsonencode({})` to empty it. (The docs
previously claimed removal reset it; that was wrong.)

**Neither `yottabot_role` nor `yottabot_group` manages its attachments.** Role
policy/group attachments and group membership are separate API endpoints, and
they are also written by the console, by SCIM, and by an SSO provider's `groups`
claim on every login. Modelling them as attributes would make Terraform the sole
authority over every one of them and delete anything created elsewhere on the
next apply — the trap AWS split apart into `aws_iam_role_policy_attachment`.
A role's `users`, `groups` and `policies` counts are readable so a config can
still assert on them.

A group's `permissions` set is different, and *is* owned: it is replaced
wholesale on every update, so the set in config is the complete grant. An empty
set clears every permission rather than leaving them alone.

Destroying a guardrail policy is a soft delete on the platform side. The row is
retained so audit references to it still resolve, and the name is released for
reuse — so destroy followed by apply with the same name works.

## 0.1.0 - 2026-09-03

First stable release. Identical in behaviour to `0.1.0-rc.2`, which was
installed from the Terraform Registry on a clean machine with no credentials
before this was promoted — that install is the whole reason the candidates
existed.

### Security

- `golang.org/x/crypto` to v0.52.0 and `google.golang.org/grpc` to v1.83.1,
  clearing eleven critical and high advisories. Both are indirect; every
  critical was in `x/crypto`, which reaches the module graph through a
  test-only path and is not present in the released binary. The two `grpc`
  advisories did affect it. Neither was ever installable, since only release
  candidates had been published and Terraform does not select a prerelease
  without an exact version constraint.

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
