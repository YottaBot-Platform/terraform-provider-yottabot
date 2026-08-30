# Support

## Community support

For questions, bugs, and feature requests about the provider itself, open a
[GitHub issue](../../issues). Issues are answered on a best-effort basis by
maintainers, and there is no response-time commitment.

Before opening one, please include the provider version, the Terraform CLI
version, the relevant configuration with secrets redacted, and the output of the
failing command. `TF_LOG=DEBUG` output is often what makes a report actionable —
redact it before posting, as it can contain endpoint URLs and request bodies.

## Commercial support

YottaBot customers should use their existing support channel for anything
involving the YottaBot service — entitlements, estate configuration, API
behavior, or an incident. That channel has response-time commitments; GitHub
issues do not.

Use commercial support, not a public issue, when a report would require sharing
your endpoint, account identifiers, or configuration you would not publish.

## What belongs where

| You want to | Go to |
| --- | --- |
| Report a provider bug or request a resource | a GitHub issue here |
| Report a security vulnerability | [SECURITY.md](SECURITY.md) — **not** an issue |
| Ask why a YottaBot API call behaves as it does | commercial support |
| Get help with entitlements or your estate | commercial support |
| Propose a change | a pull request — see [CONTRIBUTING.md](CONTRIBUTING.md) |

## Versions

Support is offered for the latest released version. If you are on an older one,
the first request will usually be to reproduce on the latest release.
