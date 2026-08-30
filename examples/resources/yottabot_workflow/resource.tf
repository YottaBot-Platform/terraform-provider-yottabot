resource "yottabot_workflow" "nightly_repo_audit" {
  name        = "nightly-repo-audit"
  description = "Runs the repository auditor every Monday morning."
  status      = "available"

  # Write "cron", not "schedule". The API accepts "schedule" and normalizes it
  # to "cron" on write, so config saying "schedule" would diff on every plan
  # with no apply able to resolve it. The provider refuses the alias.
  trigger       = "cron"
  cron_schedule = "0 7 * * 1"

  # Compared semantically: key order and whitespace do not produce diffs.
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
