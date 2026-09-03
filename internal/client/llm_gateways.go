package client

import "context"

const llmGatewaysPath = "/v1/agent-platform/llm-gateways"

// LLMGateway mirrors an `llm_gateways` row — a configured route to an upstream
// inference provider.
//
// Two fields are easy to confuse and mean different things. `Provider` is the
// UPSTREAM provider the gateway talks to (anthropic, bedrock, …). `Vendor` is
// the gateway's owner/implementer, which is YottaBot for the managed default.
// Only the first is writable; the second is computed.
type LLMGateway struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Provider      string `json:"provider"`
	Endpoint      string `json:"endpoint"`
	AuthMode      string `json:"auth_mode"`
	CredentialRef string `json:"credential_ref"`
	Status        string `json:"status"`

	SupportsStreaming  bool   `json:"supports_streaming"`
	SupportsToolCalls  bool   `json:"supports_tool_calls"`
	SupportsJSONMode   bool   `json:"supports_json_mode"`
	RateLimitPerMinute int    `json:"rate_limit_per_minute"`
	MonthlyBudgetCents int64  `json:"monthly_budget_cents"`
	BudgetPolicy       string `json:"budget_policy"`

	// Computed. None of these are writable on either route.
	Vendor           *string `json:"vendor"`
	HealthStatus     string  `json:"health_status"`
	LastCheckedAt    *string `json:"last_checked_at"`
	LastError        string  `json:"last_error"`
	ModelsCount      int     `json:"models_count"`
	LastInvokedAt    *string `json:"last_invoked_at"`
	CatalogID        *string `json:"catalog_id,omitempty"`
	IsManagedDefault bool    `json:"is_managed_default"`
	CreatedByUserID  *string `json:"created_by_user_id,omitempty"`
	CreatedAt        string  `json:"created_at"`
	ModifiedAt       string  `json:"modified_at"`
}

// LLMGatewayCreate is the POST body. Name and Provider are required.
//
// The three supports_* flags are pointers so an omitted one takes the column
// default (true) rather than being written as false.
type LLMGatewayCreate struct {
	Name               string `json:"name"`
	Description        string `json:"description,omitempty"`
	Provider           string `json:"provider"`
	Endpoint           string `json:"endpoint,omitempty"`
	AuthMode           string `json:"auth_mode,omitempty"`
	CredentialRef      string `json:"credential_ref,omitempty"`
	Status             string `json:"status,omitempty"`
	SupportsStreaming  *bool  `json:"supports_streaming,omitempty"`
	SupportsToolCalls  *bool  `json:"supports_tool_calls,omitempty"`
	SupportsJSONMode   *bool  `json:"supports_json_mode,omitempty"`
	RateLimitPerMinute int    `json:"rate_limit_per_minute,omitempty"`
	MonthlyBudgetCents int64  `json:"monthly_budget_cents,omitempty"`
	BudgetPolicy       string `json:"budget_policy,omitempty"`
}

// LLMGatewayUpdate is the PATCH body.
//
// NO PROVIDER FIELD, and that is not an oversight on this side: the service
// says so itself — "Provider is intentionally not updatable (changing it is a
// new gateway)". The resource carries RequiresReplace on that attribute for the
// same reason.
//
// Nil preserves. The service updates with COALESCE, so an absent field arrives
// as SQL NULL and keeps the stored value; a removed optional attribute has to
// send a pointer to "" to clear it. Note the asymmetry with
// guardrail_policies: there, `name` is guarded by NULLIF so an empty name is
// harmless. Here it is NOT — the service's own comment says empty-string text
// fields are legal updates — so an empty name would blank the row. The
// resource marks name Required, which is what stops that ever being sent.
type LLMGatewayUpdate struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Endpoint      *string `json:"endpoint"`
	AuthMode      *string `json:"auth_mode"`
	CredentialRef *string `json:"credential_ref"`
	Status        *string `json:"status"`

	SupportsStreaming  *bool   `json:"supports_streaming"`
	SupportsToolCalls  *bool   `json:"supports_tool_calls"`
	SupportsJSONMode   *bool   `json:"supports_json_mode"`
	RateLimitPerMinute *int    `json:"rate_limit_per_minute"`
	MonthlyBudgetCents *int64  `json:"monthly_budget_cents"`
	BudgetPolicy       *string `json:"budget_policy"`
}

func (c *Client) CreateLLMGateway(ctx context.Context, in LLMGatewayCreate) (*LLMGateway, error) {
	var out LLMGateway
	if err := c.Post(ctx, llmGatewaysPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetLLMGateway(ctx context.Context, id string) (*LLMGateway, error) {
	var out LLMGateway
	if err := c.Get(ctx, joinPath(llmGatewaysPath, id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateLLMGateway(ctx context.Context, id string, in LLMGatewayUpdate) (*LLMGateway, error) {
	var out LLMGateway
	if err := c.Patch(ctx, joinPath(llmGatewaysPath, id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteLLMGateway(ctx context.Context, id string) error {
	return c.Delete(ctx, joinPath(llmGatewaysPath, id))
}
