# Contributing

Thanks for considering a contribution. This provider wraps YottaBot's public
`/v1` REST API; it has no backend of its own, which shapes most of what follows.

## Before you start

**If your change needs an API change, it will not land here first.** The provider
cannot ship a resource for a route that does not exist in released YottaBot — the
acceptance tests would fail against every real estate. Open an issue describing
what you need from the API and we will tell you whether it is planned.

For anything larger than a bug fix, open an issue first. It is cheaper than
finding out in review that a resource cannot converge.

## Development

Requires Go 1.25+ and, for acceptance tests, Terraform 1.0+.

```shell
go build ./...
go test ./...
gofmt -l .        # must print nothing
go vet ./...
```

Run a local build through Terraform with a `dev_overrides` block in
`~/.terraformrc` — see the README's Development section.

## The one rule that matters: plans must converge

This is the failure mode specific to a provider, and it is not caught by the
compiler.

If config can express a value the server will normalize, transform, or refuse to
clear, then `terraform plan` will show a diff, `terraform apply` will not resolve
it, and the next plan will show the same diff — forever. There is no error, and
the practitioner has no way out.

Before writing a schema, work out against the real route:

- Which fields does the update route treat as "preserve" when empty, rather than
  "clear"? Those cannot be cleared in place.
- Which fields cannot be changed at all? Those are replace-only.
- Does the server normalize any input? If so, either send the canonical form or
  refuse the alias.

Then pick one of: refuse the input at validation time with an error explaining
why; mark the field replace-only; or adopt the server's value. Never ship a
permanent diff. The README's "Behavior worth knowing before you plan" section
documents the ones already found — they are precedents, not defects.

## Tests

**Every change ships with a test.** Schema changes get a schema test, client
changes get a client test, and a behavior change gets a test that fails before
the fix and passes after. There is no "too small to test" here.

Prefer `httptest` over acceptance tests where it can answer the question — it is
faster, needs no estate, and runs in CI. Acceptance tests are for behavior that
only Terraform's own lifecycle exercises.

Acceptance tests create and destroy real resources against a real YottaBot
deployment. They are gated behind `TF_ACC=1`. **Never point them at
production.** A test must clean up after itself, including on failure.

### OpenTofu

The acceptance tests **cannot** verify OpenTofu. terraform-plugin-testing
registers the provider under a synthetic address that OpenTofu rejects — it
resolves against `registry.opentofu.org` and refuses the legacy `-` namespace —
so the run fails at init, before the provider starts. That is a harness
limitation, not a provider incompatibility, and it is why the compatibility
claim needs a check that does not use the harness:

```shell
YOTTABOT_ENDPOINT=... YOTTABOT_TOKEN=... scripts/opentofu-lifecycle.sh tofu
```

It drives one resource through apply, a re-plan that must come back empty,
import into a fresh state, another empty plan, and destroy. Run it against both
ends of the supported OpenTofu range before changing the range in the README —
the floor there is a tested claim, not one derived from protocol support. The
script takes any CLI path, so it works for Terraform too.

## Style

- Comments explain *why* — a constraint, a tradeoff, an API quirk you worked
  around. Don't narrate what the next line does.
- Errors explain the fix. Passing a raw server constraint name through to the
  practitioner is not an error message.
- Sensitive values stay sensitive. Nothing that returns one-shot secret material
  becomes a resource.
- Follow the product's nouns. The engine coordinating a run is an
  **orchestrator**, not a "runtime".

## Dependencies

Every direct dependency is a HashiCorp Terraform plugin library, and that is
the whole list. It is deliberate: this binary is downloaded and run by
customers, so a new direct dependency is a supply-chain decision for all of
them. A PR adding one — particularly one outside the Terraform plugin
ecosystem — needs to justify it.

## Pull requests

- One logical change per PR.
- Include a changelog fragment for anything user-visible, categorized as
  breaking, deprecation, feature, enhancement, bug fix, security, or maintenance.
- Say in the description which diff semantics you verified against the real API.
- CI must pass. Fork PRs run with read-only tokens and no release credentials.

By contributing you agree your contributions are licensed under the repository's
license (MPL-2.0).
