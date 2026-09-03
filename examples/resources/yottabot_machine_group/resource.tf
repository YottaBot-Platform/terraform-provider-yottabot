resource "yottabot_machine_group" "ci_runners" {
  name        = "ci-runners"
  description = "Service accounts for CI pipelines"
}

# Membership is not managed here — members are added through their own endpoint
# and their lifecycle is driven by provisioning. The count is readable so a
# check can still assert on it.
output "ci_runner_count" {
  value = yottabot_machine_group.ci_runners.member_count
}
