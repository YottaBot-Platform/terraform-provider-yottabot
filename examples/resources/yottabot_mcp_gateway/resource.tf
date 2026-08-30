resource "yottabot_mcp_gateway" "github" {
  name        = "github-mcp"
  description = "Routes MCP tool calls to the GitHub MCP server."
  endpoint    = "https://mcp.example.com/github"
  transport   = "streamable-http"
  status      = "available"
}
