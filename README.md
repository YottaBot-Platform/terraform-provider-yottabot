# Terraform Provider for YottaBot

Manage YottaBot **definitions** — agents, workflows, Context providers, and MCP
gateway/tool catalog entries — as Terraform resources.

The provider wraps YottaBot's existing, permission-gated `/v1` REST API. There is
no provider-specific backend: anything Terraform can do here, your operators can
do through the console, the CLI, or the API, and the audit trail records it
the same way.

## What this provider manages, and what it does not

YottaBot is a control plane. This provider manages the definitions inside it —
the agents, workflows, and registrations that make up your YottaBot
configuration.

It does **not** manage your infrastructure. Your workloads live in your own
cloud accounts, managed by your own Terraform. The two meet at
`yottabot_context_provider`: you register an account you own, and YottaBot
discovers what is in it. That registration is a reference — this provider never
mints or stores credentials for your cloud.

```hcl
provider "aws" {}        # your account, your workloads — not this provider

provider "yottabot" {}   # your YottaBot control plane — this provider
```

Three consequences follow from that boundary, and each is documented in its own
section below: there is no resource that *runs* an agent or workflow, there is
no resource that mints a credential, and `yottabot_context_provider` behaves
differently from the other four because it registers something you own rather
than creating something we own.

> **Status: pre-release.** The provider is feature-complete for its five v1
> resources and covered by unit tests, but it is **not yet published to the
> Terraform Registry**. Until the first release, build from source (see
> [Development](#development)). Version numbers, the `source` address, and the
> release artifacts described below are the intended contract, not a shipped one.

## Contents

- [What this provider manages, and what it does not](#what-this-provider-manages-and-what-it-does-not)
- [Requirements](#requirements)
- [Compatibility](#compatibility)
- [Quick start](#quick-start)
- [Resources](#resources)
- [Authentication](#authentication)
- [Required permissions](#required-permissions)
- [Importing existing resources](#importing-existing-resources)
- [Behavior worth knowing before you plan](#behavior-worth-knowing-before-you-plan)
- [What this provider deliberately does not do](#what-this-provider-deliberately-does-not-do)
- [Development](#development)
- [Versioning](#versioning)
- [Contributing](#contributing)
- [Support](#support)
- [Security](#security)
- [License](#license)

## Requirements

| Component | Version |
| --- | --- |
| Terraform CLI | 1.0 or later |
| OpenTofu | 1.6 or later |
| Go (to build from source) | 1.25 or later |
| YottaBot | see [Compatibility](#compatibility) |

The provider implements **Terraform Plugin Protocol v6** via
[terraform-plugin-framework](https://github.com/hashicorp/terraform-plugin-framework).
Protocol v6 is why Terraform 1.0 is the floor.

## Compatibility

| Provider version | YottaBot API | Notes |
| --- | --- | --- |
| 0.x | `/v1` as of the matching YottaBot release | Pre-1.0. Schema and behavior may change between minors; pin an exact version. |

A published compatibility matrix — provider version against tested YottaBot
releases and Terraform CLI versions — ships with the first stable release. Until
then, run the provider against the YottaBot release it was built from.

## Quick start

```hcl
terraform {
  required_providers {
    yottabot = {
      source  = "YottaBot-Platform/yottabot"
      version = "~> 0.1"
    }
  }
}

provider "yottabot" {
  endpoint = "https://yottabot.example.com"
  token    = var.yottabot_token
}

resource "yottabot_agent" "repo_auditor" {
  name          = "RepoAuditor"
  description   = "Reviews repository changes and produces concise risk findings."
  status        = "available"
  model         = "claude-opus-5"
  system_prompt = file("${path.module}/prompts/repo_auditor.md")

  tool_ids = [yottabot_mcp_tool.github.id]

  env = {
    LOG_LEVEL = "info"
  }
}

resource "yottabot_workflow" "nightly_repo_audit" {
  name          = "nightly-repo-audit"
  status        = "available"
  trigger       = "cron"
  cron_schedule = "0 7 * * 1"

  definition_json = jsonencode({
    steps = [
      {
        name   = "audit"
        type   = "agent_call"
        agent  = yottabot_agent.repo_auditor.id
        output = "findings"
      }
    ]
  })
}
```

```shell
terraform init
terraform plan
```

## Resources

| Terraform type | YottaBot API | Delete semantics |
| --- | --- | --- |
| `yottabot_agent` | `/v1/agent-platform/agents` | Deletes the agent and its linked user |
| `yottabot_workflow` | `/v1/agent-platform/workflows` | Deletes the workflow definition |
| `yottabot_context_provider` | `/v1/context/parent-handles` | **Retires** — see [below](#destroy-retires-a-context-provider-it-does-not-delete-it) |
| `yottabot_mcp_gateway` | `/v1/agent-platform/mcp-gateways` | Deletes the catalog row |
| `yottabot_mcp_tool` | `/v1/agent-platform/tools` (`type = "mcp"`) | Deletes the catalog row |

Per-attribute reference documentation is generated with
[`tfplugindocs`](https://github.com/hashicorp/terraform-plugin-docs) and published
to the Terraform Registry with each release. Until the first release, read the
schemas in [`internal/provider/`](internal/provider/).

## Authentication

Two credential paths. Both are configured on the `provider` block, and every
field falls back to an environment variable.

```hcl
provider "yottabot" {
  endpoint = "https://yottabot.example.com"

  # Path 1 — static bearer token. Convenient for local and manual runs.
  # Marked sensitive; never written to logs.
  token = var.yottabot_token

  # Path 2 — service-account OAuth client credentials with an RFC 7523
  # Ed25519 client assertion. Preferred for CI and any unattended apply:
  # the audit trail then names the service account rather than whichever
  # human happened to run it. Tokens are cached in memory and refreshed
  # before expiry.
  #
  # user_id         = var.yottabot_service_account_user_id
  # kid             = var.yottabot_service_account_kid
  # private_key_pem = var.yottabot_service_account_private_key_pem
  # token_url       = "https://yottabot.example.com/api/machine-auth/v1/oauth/token"
}
```

| Field | Environment variable | Compatibility alias |
| --- | --- | --- |
| `endpoint` | `YOTTABOT_ENDPOINT` | `YOTTA_ENDPOINT` |
| `token` | `YOTTABOT_TOKEN` | `YOTTA_TOKEN` |
| `user_id` | `YOTTABOT_USER_ID` | `YOTTA_USER_ID` |
| `kid` | `YOTTABOT_KID` | `YOTTA_KID` |
| `private_key_pem` | `YOTTABOT_PRIVATE_KEY_PEM` | `YOTTA_PRIVATE_KEY_PEM` |
| `token_url` | `YOTTABOT_TOKEN_URL` | `YOTTA_TOKEN_URL` |

Precedence is config, then `YOTTABOT_*`, then `YOTTA_*`. The `YOTTA_*` aliases
exist for workspaces already exporting the YottaBot CLI's variables. **The canonical
`YOTTABOT_*` name wins when both are set**, so a stale `YOTTA_ENDPOINT` left in a
shell cannot silently redirect an apply at the wrong estate.

`token_url` defaults to `<endpoint>/api/machine-auth/v1/oauth/token` when a
service account is configured.

Agent attestation tokens are **not** valid provider credentials. Terraform is an
operator/integration client, not an agent runtime.

## Required permissions

The full v1 policy for a service account running every resource in this provider,
scoped to one tenant:

```text
agents:read     agents:write     agents:delete
workflows:read  workflows:write  workflows:delete
context_parent_handles:read      context_parent_handles:write
mcp_gateways:read                mcp_gateways:write   mcp_gateways:delete
tools:read      tools:write      tools:delete
users:write
```

`users:write` is the one that surprises people. Creating an agent mints its
linked `kind='agent'` user, so an otherwise-correct `agents:write` policy still
returns 403. Grant only the resources you actually manage.

## Importing existing resources

```shell
terraform import yottabot_agent.repo_auditor           <agent-uuid>
terraform import yottabot_workflow.nightly_repo_audit  <workflow-uuid>
terraform import yottabot_context_provider.github      <provider-uuid>
terraform import yottabot_mcp_gateway.github           <gateway-uuid>
terraform import yottabot_mcp_tool.github              <tool-uuid>
```

Import IDs are UUIDs. Names are not unique within a tenant, so a name-based
import would be ambiguous.

## Behavior worth knowing before you plan

These are deliberate design decisions, not defects. Each one exists because the
alternative produces a plan that never converges.

### `status` is adopted, not enforced, when omitted

Leaving `status` out of config means Terraform accepts whatever the server has.
Publishing an agent from the console therefore does not show up as drift. Declare
`status` explicitly if you want Terraform to own the lifecycle.

### Removing `prompt_id` replaces the agent

The update route treats an empty `prompt_id` as "preserve", so it cannot be
cleared in place — an in-place removal would show the same diff on every plan
forever. Replacement is the honest behavior, and the plan output says so.

### `trigger = "schedule"` is refused

The API accepts `schedule` and normalizes it to `cron` on write. Config saying
`schedule` against a row storing `cron` would diff on every plan with no apply
able to resolve it, so the provider rejects it up front. Write `cron`.

### `definition_json` is compared semantically

Key order and whitespace do not produce diffs. Invalid or unrunnable definitions
are refused at apply time with every problem listed at once, rather than one per
run.

### `yottabot_mcp_tool` calls the vendor field `vendor`, not `provider`

The wire field is `provider`, but `provider` is a Terraform meta-argument and
cannot be a resource attribute — the framework rejects such a schema outright.
The rename is Terraform-side only; the request body still sends `provider`.

### `destroy` retires a Context provider, it does not delete it

`DELETE /v1/context/parent-handles/{id}` sets `state = 'retired'`. The row stays
in the database, still holding its `UNIQUE (account_id, type, external_id)` key.
Two consequences:

- `terraform destroy` followed by `terraform apply` on the same
  `type` + `external_id` **fails** with a unique-constraint refusal.
- `discoverer` is replace-only, so changing it destroys and recreates with the
  *same* `type` + `external_id` and therefore **always** fails. In practice
  `discoverer` cannot currently be changed through Terraform.

The provider recognizes this refusal and explains it rather than passing through
the raw constraint name. Recover by adopting the existing row:

```shell
terraform import yottabot_context_provider.example <provider-uuid>
```

then set `state = "active"` in config and apply.

## What this provider deliberately does not do

- **No execution.** Terraform manages definitions, not runs. Triggering a
  workflow or agent is a side effect, and side effects do not belong in a
  desired-state tool.
- **No credential minting.** Minting returns a one-shot private key that would
  land in Terraform state in cleartext. Credentials stay an Identity /
  service-account concern.
- **No MCP server deployment.** The provider registers YottaBot routing and
  catalog rows. Rolling out the MCP server itself belongs in your own infra
  modules.
- **No Context discovery polling.** Creating a `yottabot_context_provider`
  registers the parent handle; it does not poll or assert on discovered children.

## Development

```shell
go build ./...
go test ./...
```

To run a locally built provider, add a `dev_overrides` block to `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "YottaBot-Platform/yottabot" = "/path/to/your/gobin"
  }

  direct {}
}
```

The path is the **directory** holding the built binary, not the binary itself.
With an override in place Terraform skips `init` for this provider — run
`plan`/`apply` directly, and expect a warning saying so.

Acceptance tests talk to a real YottaBot estate, create and destroy real
resources, and are gated behind `TF_ACC=1`. Never point them at production.

## Versioning

Releases are immutable SemVer tags (`vX.Y.Z`); release candidates use
`vX.Y.Z-rc.N`. Breaking changes to provider configuration, resource schema,
import IDs, or state behavior wait for a major release. Deprecations stay for at
least one stable minor and ship with a migration path, except where an urgent
security fix makes that impossible.

Published versions are never replaced or re-tagged. Defects are corrected with a
new patch release.

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for
the development workflow, test expectations, and changelog fragment format.
Participation is governed by our [Code of Conduct](CODE_OF_CONDUCT.md).

## Support

See [SUPPORT.md](SUPPORT.md) for the boundary between community support (GitHub
issues, best effort) and commercial support for YottaBot customers.

## Security

Do not report security issues through public GitHub issues. See
[SECURITY.md](SECURITY.md) for private vulnerability reporting.

## License

Licensed under the Mozilla Public License 2.0. See [LICENSE](LICENSE).

The license covers this provider's source code. It does not grant a license to
operate the YottaBot server software, which is subject to separate commercial
terms.
