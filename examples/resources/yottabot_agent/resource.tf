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
