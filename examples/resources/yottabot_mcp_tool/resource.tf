resource "yottabot_mcp_tool" "github" {
  name        = "github"
  description = "GitHub repository and pull request operations."
  status      = "available"
  version     = "1.0.0"

  # The API field for this is `provider`, but `provider` is a Terraform
  # meta-argument and cannot be a resource attribute — the framework rejects
  # such a schema outright. The rename is Terraform-side only; the request body
  # still sends `provider`.
  vendor = "GitHub"

  config_json = jsonencode({
    gateway_id = yottabot_mcp_gateway.github.id
  })

  secret_ref = var.github_tool_secret_ref
}
