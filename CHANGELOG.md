# Changelog

All notable changes to this provider are documented here. This project follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Published versions are immutable. A defect is corrected with a new patch
release, never by replacing a published tag or asset.

## Unreleased

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
