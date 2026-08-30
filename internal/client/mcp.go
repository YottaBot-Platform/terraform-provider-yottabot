package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

const (
	mcpGatewaysPath = "/v1/agent-platform/mcp-gateways"
	toolsPath       = "/v1/agent-platform/tools"

	// ToolTypeMCP is the only tool type this provider manages. The tools
	// catalog holds other types; yottabot_mcp_tool is deliberately narrow.
	ToolTypeMCP = "mcp"
)

// MCPGateway mirrors an `mcp_gateways` row — the routeable endpoint the
// platform can call. Registering it here is catalog configuration; rolling out
// the MCP server itself is the customer's infra concern.
type MCPGateway struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Endpoint  string `json:"endpoint"`
	Transport string `json:"transport"`
	Status    string `json:"status"`

	// HealthStatus is operational health of deployed instances, distinct from
	// the lifecycle `Status` (bot/181). Computed — never an input.
	HealthStatus string `json:"health_status"`
	Description  string `json:"description"`
	// ToolsCount serializes as "tools", NOT "tools_count". Reading the wire
	// shape rather than the column name is the only way to get this right.
	ToolsCount      int     `json:"tools"`
	CreatedByUserID *string `json:"created_by_user_id,omitempty"`
	CreatedAt       string  `json:"created_at"`
	ModifiedAt      string  `json:"modified_at"`
}

// MCPGatewayCreate is the POST body — plain strings, name + endpoint required.
type MCPGatewayCreate struct {
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Transport   string `json:"transport,omitempty"`
	Status      string `json:"status,omitempty"`
	Description string `json:"description"`
}

// MCPGatewayUpdate is the PATCH body. Unlike every other route in this
// provider, it takes NULLABLE POINTERS and the repo leaves nil fields
// untouched — so omission is the preserve signal and a pointer to "" writes an
// empty string. name and endpoint additionally reject an explicit "" at the
// service layer, which is fine: both are Required in Terraform.
type MCPGatewayUpdate struct {
	Name        *string `json:"name"`
	Endpoint    *string `json:"endpoint"`
	Transport   *string `json:"transport"`
	Status      *string `json:"status"`
	Description *string `json:"description"`
}

func (c *Client) CreateMCPGateway(ctx context.Context, in MCPGatewayCreate) (*MCPGateway, error) {
	var out MCPGateway
	if err := c.Post(ctx, mcpGatewaysPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetMCPGateway(ctx context.Context, id string) (*MCPGateway, error) {
	var out MCPGateway
	if err := c.Get(ctx, joinPath(mcpGatewaysPath, id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateMCPGateway(ctx context.Context, id string, in MCPGatewayUpdate) (*MCPGateway, error) {
	var out MCPGateway
	if err := c.Patch(ctx, joinPath(mcpGatewaysPath, id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteMCPGateway(ctx context.Context, id string) error {
	return c.Delete(ctx, joinPath(mcpGatewaysPath, id))
}

// MCPTool mirrors a `tools` row of type `mcp` — the handle agents attach to and
// workflows invoke.
type MCPTool struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description *string         `json:"description"`
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	Version     *string         `json:"version"`
	Provider    *string         `json:"provider"`
	Config      json.RawMessage `json:"config"`
	SecretRef   *string         `json:"secret_ref"`
	Tags        *string         `json:"tags"`
	OwnerUserID *string         `json:"owner_user_id"`
	CreatedAt   string          `json:"created_at"`
	ModifiedAt  string          `json:"modified_at"`
}

// MCPToolInput is the create/update body.
//
// UPDATE semantics, once more with feeling:
//
//	name / type / status                       COALESCE(NULLIF($n,'')) → "" preserves
//	description / version / secret_ref /
//	tags / provider                            COALESCE($n, col)       → "" clears
//	config                                     COALESCE($n::jsonb)     → null preserves
type MCPToolInput struct {
	Name        string          `json:"name,omitempty"`
	Description *string         `json:"description,omitempty"`
	Type        string          `json:"type,omitempty"`
	Status      string          `json:"status,omitempty"`
	Version     *string         `json:"version,omitempty"`
	Provider    *string         `json:"provider,omitempty"`
	Config      json.RawMessage `json:"config,omitempty"`
	SecretRef   *string         `json:"secret_ref,omitempty"`
	Tags        *string         `json:"tags,omitempty"`
}

func (c *Client) CreateMCPTool(ctx context.Context, in MCPToolInput) (*MCPTool, error) {
	var out MCPTool
	if err := c.Post(ctx, toolsPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetMCPTool(ctx context.Context, id string) (*MCPTool, error) {
	var out MCPTool
	if err := c.Get(ctx, joinPath(toolsPath, id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateMCPTool(ctx context.Context, id string, in MCPToolInput) (*MCPTool, error) {
	var out MCPTool
	if err := c.Patch(ctx, joinPath(toolsPath, id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteMCPTool(ctx context.Context, id string) error {
	return c.Delete(ctx, joinPath(toolsPath, id))
}

// joinPath escapes the id so a malformed import argument cannot alter the route
// it addresses.
func joinPath(base, id string) string {
	return fmt.Sprintf("%s/%s", base, url.PathEscape(id))
}
