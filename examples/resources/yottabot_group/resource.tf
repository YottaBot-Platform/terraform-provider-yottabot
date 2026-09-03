resource "yottabot_group" "engineers" {
  name        = "engineers"
  description = "Platform engineering"

  # Replaced wholesale on every apply: this set is the complete grant, and a
  # permission added outside Terraform is removed on the next run. An empty set
  # is meaningful — it clears every permission rather than leaving them alone.
  permissions = [
    "agents:read",
    "agents:write",
    "workflows:read",
  ]
}
