resource "yottabot_context_provider" "github_org" {
  type         = "github_org"
  external_id  = "example-org"
  display_name = "Example Org GitHub"
  discoverer   = "github"

  credential_ref = var.github_context_credential_ref
  ingestion_mode = "hybrid"

  discoverer_cfg_json = jsonencode({
    include_repos = ["example-repo"]
  })
}
