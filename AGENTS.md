# AGENTS.md

Operational guide for anyone — human or AI — making changes in
`terraform-provider-yottabot`. Read this before opening a PR.

The format follows the [`AGENTS.md`](https://agents.md/) convention:
one tool-agnostic file describing how to work in the repository.

---

## 1. What this repo is

The **Terraform provider for YottaBot**. A standalone Go module that speaks
YottaBot's public `/v1` REST API and exposes agents, workflows, Context
providers, and MCP gateway/tool catalog rows as Terraform resources.

This repository is **public and customer-facing**. Everything committed here is
read by people evaluating the product. Write accordingly.

It does **not** contain YottaBot server code, and it must never import any. The
provider is a REST client with a Terraform schema on top.

## 2. The bright line: what belongs here

| Thing | Lives in |
| --- | --- |
| Provider schema, CRUD, import, plan modifiers | **here** |
| REST client, auth (bearer + service-account assertion) | **here** |
| Generated Registry docs, examples, release workflow | **here** |
| YottaBot API routes, handlers, database, permissions | the YottaBot server (not public) |
| Helm charts that install YottaBot | [`helm-charts`](https://github.com/YottaBot-Platform/helm-charts) |
| The YottaBot CLI | not public |

**If a change needs an API change, the API change lands first.** A provider
release that depends on an unreleased route will fail acceptance tests against
every existing estate. Never reverse that order.

## 3. Quality bar

This repository is held to a **principal-engineer-level** bar, and it is graded
in public.

- **Every change ships with a test.** Schema changes get a schema test, client
  changes get a client test, behavior changes get a test that fails before the
  fix. There is no "too small to test" here.
- **Comments explain *why*.** A constraint, a tradeoff, an invariant, an API
  quirk you had to work around. Don't narrate what the next line obviously does.
- **No plan that cannot converge.** This is the single most important rule in
  the provider. If config can express a value the server will normalize,
  transform, or refuse to clear, then a plan will diff forever and no apply can
  fix it. The options, in order of preference: refuse the input at validation
  time with an error that says why; mark the field replace-only; or adopt the
  server value. Never ship the fourth option, which is a permanent diff.
- **Sensitive values stay sensitive.** `token` and `private_key_pem` are marked
  sensitive and must never reach logs, errors, or state in the clear. Anything
  that returns one-shot secret material does not become a resource.
- **Errors explain the fix.** Passing a raw server constraint name through to
  the operator is not an error message. Say what happened, why, and what to do —
  the retired-Context-provider path in the README is the reference example.

## 4. Layout

```text
main.go                        provider server entrypoint
internal/client/               REST client — one file per resource family
  auth.go, assertion.go        bearer + RFC 7523 service-account paths
internal/provider/
  provider.go                  provider schema, Resources() registration
  config.go                    config/env precedence resolution
  resource_<name>.go           one resource per file
  schema_validity_test.go      guards reserved-name and schema invariants
docs/                          generated — do not hand-edit
examples/                      used by tfplugindocs; must actually work
```

## 5. Naming

- Resource types are `yottabot_<noun>`, singular. The provider type name is
  `yottabot` and is set in exactly one place.
- Attribute names follow the API's field names **except** where Terraform
  forbids it. `provider` is a Terraform meta-argument and the framework rejects
  it outright — the MCP tool's vendor field is therefore `vendor` on the schema
  and `provider` on the wire. `schema_validity_test.go` rejects any reserved
  name; if it fails, rename the attribute, don't weaken the test.
- Follow the product's noun conventions. The engine that coordinates a run is an
  **orchestrator** (`orchestrator_id`), never a "runtime".

## 6. Adding a resource

1. Add the client methods in `internal/client/`, with tests covering the
   request body and the error mapping.
2. Add `internal/provider/resource_<name>.go` implementing Create, Read, Update,
   Delete, and ImportState. Import is by UUID.
3. **Work out the diff semantics against the real route before writing the
   schema.** Specifically: which fields does the update route `COALESCE` (so an
   empty value means "preserve", not "clear")? Which fields cannot be cleared at
   all (those are replace-only)? Does the server normalize any input? Getting
   this wrong produces a resource that can never converge, and it is not
   discoverable from the schema alone.
4. Register it in `Resources()` in `provider.go`.
5. Add the resource to the README table and write an example under `examples/`.
6. Regenerate docs. Never hand-edit `docs/`.

## 7. Testing

```shell
go test ./...          # unit — always required
```

Acceptance tests create and destroy real resources against a real YottaBot
estate. They are gated behind `TF_ACC=1` and a configured endpoint.

- **Never point acceptance tests at a production estate.**
- An acceptance test must clean up after itself, including on failure.
- A test that needs a resource the previous test destroyed will hit the
  Context-provider retirement behavior documented in the README. Design around
  it rather than reintroducing it as a surprise.

## 8. Releases

Releases are immutable SemVer tags. The full contract is in `RELEASE.md`; the
parts that constrain day-to-day work:

- Breaking changes to provider config, resource schema, import IDs, or state
  behavior wait for a **major** release. Deprecations last at least one stable
  minor and ship with a migration path.
- Every user-visible change needs a **changelog fragment** categorized as
  breaking, deprecation, feature, enhancement, bug fix, security, or maintenance.
- **Never replace a published tag or release asset.** Correct defects with a new
  patch release.

## 9. When to ask vs. act

Ask first when:

- The change requires a YottaBot API change (that PR lands first — see §2).
- The change is breaking, or would force a major version bump.
- A published resource's import ID or state shape would change.
- The right fix is a backend behavior change rather than a provider workaround.
  The retired-Context-provider recreation path is an open question of exactly
  this kind; don't resolve it unilaterally from this side.

Otherwise act, and describe the diff semantics you verified in the PR.

## 10. Commit messages

Subject is prefixed with `<username>/<kind>[/<ticket>]: <subject>`, where
`<kind>` is one of `feat`, `fix`, or `chore`, and the ticket segment is omitted
entirely when there isn't one.

- `tyang/feat: add yottabot_mcp_tool resource`
- `tyang/fix: refuse trigger="schedule" instead of diffing forever`
- `tyang/chore: regenerate docs for v0.2.0`

The prefix is per-commit attribution. Ask which engineer is the primary author
rather than copying the previous commit's prefix.

## 11. Common commands

```shell
go build ./...
go test ./...
go generate ./...              # regenerates docs/ via tfplugindocs
gofmt -l .                     # must be empty
```

Run a local build through Terraform with a `dev_overrides` block in
`~/.terraformrc` — see the README's Development section. The override path is the
directory holding the binary, not the binary itself.
