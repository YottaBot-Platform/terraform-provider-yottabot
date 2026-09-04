resource "yottabot_policy" "agent_readonly" {
  name        = "agent-readonly"
  description = "Read agents and workflows, never mutate them"

  # An ORDERED list: the server evaluates statements in this order, and a `deny`
  # short-circuits an earlier `allow`. Reordering changes what the policy means.
  statements = [
    {
      sid     = "deny-destructive"
      effect  = "deny"
      actions = ["agent_templates:write", "workflow_templates:write"]
    },
    {
      sid       = "allow-reads"
      effect    = "allow"
      actions   = ["agent_runs:read", "agent_templates:read"]
      resources = ["*"]
    },
  ]
}

# Attachments to roles are managed elsewhere; this is read-only.
output "agent_readonly_attached_to" {
  value = yottabot_policy.agent_readonly.attached
}
