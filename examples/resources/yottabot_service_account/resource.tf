resource "yottabot_group" "ci" {
  name        = "ci-runners"
  description = "Owns the CI automation principals"
}

resource "yottabot_service_account" "deployer" {
  username       = "ci-deployer"
  owner_group_id = yottabot_group.ci.id
}

# This resource does NOT hand out credentials: Terraform writes state in
# plaintext, so a private key here would be a long-lived secret in the state
# file. Mint credentials through their own surface instead.
#
# `terraform destroy` RETIRES rather than deletes — the row and its audit trail
# survive, credentials are revoked, and the username is released for reuse.
output "deployer_status" {
  value = yottabot_service_account.deployer.status
}
