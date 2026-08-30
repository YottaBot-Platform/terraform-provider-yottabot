## What this changes

<!-- One or two sentences. Link the issue if there is one. -->

## Diff semantics verified

<!--
Required for any schema change. A provider's characteristic failure is a plan
that never converges, and it is invisible to the compiler. Say what you checked
against the real API:

- fields the update route treats as "preserve" when empty (cannot be cleared)
- fields that cannot change at all (replace-only)
- values the server normalizes (send canonical, or refuse the alias)

Write "n/a — no schema change" if that is the case.
-->

## Tests

<!--
What you added, and how you know it fails without the change.
`httptest` where possible; acceptance tests only for what Terraform's own
lifecycle exercises.
-->

- [ ] `go test ./...` passes
- [ ] `gofmt -l .` prints nothing
- [ ] `go vet ./...` is clean

## Changelog

<!-- Category: breaking / deprecation / feature / enhancement / bug fix / security / maintenance. Write "none" for internal-only changes. -->

## Breaking changes

<!--
Provider config, resource schema, import IDs, and state shape are a contract.
If this changes one, say so here and describe the migration. Breaking changes
wait for a major release.
-->
