resource "yottabot_role" "sre" {
  name        = "sre"
  description = "On-call engineers"
}

# Policy and group attachments are not managed by this resource — they are
# separate API endpoints, and owning them here would delete any attachment made
# outside this config. The counts are readable, so a check can assert on them.
output "sre_attached_policies" {
  value = yottabot_role.sre.policies
}
