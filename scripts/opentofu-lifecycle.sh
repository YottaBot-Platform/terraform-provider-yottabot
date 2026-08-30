#!/usr/bin/env bash
#
# Drive one resource through a full lifecycle with a chosen CLI binary.
#
# This exists because the acceptance tests cannot verify OpenTofu.
# terraform-plugin-testing registers the provider under a synthetic address and
# OpenTofu resolves it against registry.opentofu.org, so `tofu` refuses it with
#
#   Invalid provider namespace: the legacy provider namespace "-" can be used
#   only with hostname registry.opentofu.org
#
# before the provider is ever started. That is a limitation of the test harness,
# not of the provider — which is exactly why the compatibility claim needs a
# check that does not use the harness.
#
# Usage:
#   YOTTABOT_ENDPOINT=... YOTTABOT_TOKEN=... scripts/opentofu-lifecycle.sh [path-to-cli]
#
# Creates and destroys a real agent. Point it at a disposable deployment.
set -euo pipefail

CLI="${1:-tofu}"
command -v "$CLI" >/dev/null || { echo "not found: $CLI" >&2; exit 1; }
: "${YOTTABOT_ENDPOINT:?set YOTTABOT_ENDPOINT}"
: "${YOTTABOT_TOKEN:?set YOTTABOT_TOKEN}"

root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "==> $("$CLI" version | head -1)"
go build -o "$work/bin/terraform-provider-yottabot" "$root"

cat > "$work/cli.tfrc" <<EOF
provider_installation {
  dev_overrides { "YottaBot-Platform/yottabot" = "$work/bin" }
  direct {}
}
EOF

cat > "$work/main.tf" <<'EOF'
terraform {
  required_providers {
    yottabot = { source = "YottaBot-Platform/yottabot" }
  }
}
resource "yottabot_agent" "probe" {
  name        = "opentofu-lifecycle-probe"
  description = "created and destroyed by scripts/opentofu-lifecycle.sh"
  status      = "draft"
}
output "id" { value = yottabot_agent.probe.id }
EOF

cd "$work"
export TF_CLI_CONFIG_FILE="$work/cli.tfrc"

echo "==> apply"
"$CLI" apply -auto-approve -no-color >/dev/null

echo "==> re-plan must be empty"
"$CLI" plan -no-color -detailed-exitcode >/dev/null && echo "    clean" || {
  echo "    FAIL: plan is not empty after apply — a diff that no apply can settle" >&2
  "$CLI" plan -no-color >&2; exit 1
}

id="$("$CLI" output -raw id)"
echo "==> import $id into a fresh state"
rm -f terraform.tfstate
"$CLI" import -no-color yottabot_agent.probe "$id" >/dev/null

echo "==> plan after import must be empty"
"$CLI" plan -no-color -detailed-exitcode >/dev/null && echo "    clean" || {
  echo "    FAIL: import produced state that does not match config" >&2; exit 1
}

echo "==> destroy"
"$CLI" destroy -auto-approve -no-color >/dev/null
echo "==> OK — $("$CLI" version | head -1) drives the full lifecycle"
