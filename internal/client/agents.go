package client

import (
	"context"
	"fmt"
	"net/url"
)

// agentsPath is the collection route. Kept in one place so a future prefix
// change is a one-line edit rather than a grep.
const agentsPath = "/v1/agent-platform/agents"

// Agent mirrors the agent row the API returns.
//
// Only the fields the provider manages or surfaces are declared — the wire
// shape carries more (render provenance, labels, annotations, template links),
// and adding them here would imply Terraform manages them. Unknown fields
// decode away harmlessly.
//
// `mint_credential` is deliberately absent in both directions: it returns a
// one-shot private key, and any field the provider can read is a field that can
// land in state.
type Agent struct {
	ID          string  `json:"id"`
	UserID      *string `json:"user_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`

	Model        *string `json:"model"`
	ModelID      *string `json:"model_id"`
	SystemPrompt *string `json:"system_prompt"`
	Tags         *string `json:"tags"`

	OrchestratorID *string `json:"orchestrator_id"`

	ToolIDs            []string          `json:"tool_ids"`
	DataSourceIDs      []string          `json:"data_source_ids"`
	SecretIDs          []string          `json:"secret_ids"`
	GuardrailPolicyIDs []string          `json:"guardrail_policy_ids"`
	PromptID           *string           `json:"prompt_id"`
	Env                map[string]string `json:"env"`

	CreatedAt  string `json:"created_at"`
	ModifiedAt string `json:"modified_at"`
}

// AgentInput is the create/update body.
//
// The pointer/slice choices encode the API's update semantics exactly, and they
// are not interchangeable — see repo.go's UPDATE statement:
//
//	description / model / system_prompt / tags  COALESCE($n, col)
//	    → JSON null PRESERVES; "" sets empty. A *string of nil omits the key
//	      (preserve); a pointer to "" clears.
//	name / status / prompt_id / orchestrator_id  COALESCE(NULLIF($n,''), col)
//	    → "" PRESERVES. These cannot be cleared through this route at all.
//	tool_ids / data_source_ids / secret_ids /
//	guardrail_policy_ids / env                   COALESCE($n, col)
//	    → null preserves; [] or {} CLEARS; populated replaces.
//
// Terraform needs convergence, not patch semantics: whatever the config
// declares must become true after one apply. So the resource always sends the
// full desired state — empty slices and an empty map rather than nil — and the
// omitempty tags below exist only for the fields where omission is the
// meaningful signal.
type AgentInput struct {
	Name        string  `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      string  `json:"status,omitempty"`

	Model        *string `json:"model,omitempty"`
	SystemPrompt *string `json:"system_prompt,omitempty"`
	Tags         *string `json:"tags,omitempty"`
	PromptID     *string `json:"prompt_id,omitempty"`

	// No omitempty: an empty slice is the CLEAR signal and must reach the
	// server. With omitempty, removing every tool from config would silently
	// preserve the old list and diff forever.
	ToolIDs            []string          `json:"tool_ids"`
	DataSourceIDs      []string          `json:"data_source_ids"`
	SecretIDs          []string          `json:"secret_ids"`
	GuardrailPolicyIDs []string          `json:"guardrail_policy_ids"`
	Env                map[string]string `json:"env"`
}

// CreateAgent creates an agent.
//
// Note for callers debugging a 403: this route needs `users:write` in addition
// to `agents:write`, because creating an agent mints its linked kind='agent'
// user. newAPIError says so on every 403.
func (c *Client) CreateAgent(ctx context.Context, in AgentInput) (*Agent, error) {
	var out Agent
	if err := c.Post(ctx, agentsPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAgent reads one agent by UUID. A missing agent yields an APIError for
// which IsNotFound reports true — the signal Read turns into "remove from
// state" rather than an error.
func (c *Client) GetAgent(ctx context.Context, id string) (*Agent, error) {
	var out Agent
	if err := c.Get(ctx, agentPath(id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAgent patches an agent.
func (c *Client) UpdateAgent(ctx context.Context, id string, in AgentInput) (*Agent, error) {
	var out Agent
	if err := c.Patch(ctx, agentPath(id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAgent removes an agent.
func (c *Client) DeleteAgent(ctx context.Context, id string) error {
	return c.Delete(ctx, agentPath(id))
}

// agentPath escapes the id so a malformed import argument cannot alter the
// route it addresses.
func agentPath(id string) string {
	return fmt.Sprintf("%s/%s", agentsPath, url.PathEscape(id))
}
