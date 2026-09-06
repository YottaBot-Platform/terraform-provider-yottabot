package client

import "context"

const modelsPath = "/v1/agent-platform/models"

// Model mirrors a `models` row — a model REGISTERED by this account, not a
// product-catalog entry.
//
// The distinction matters for what this client may do. Every row here is
// account-scoped with an owner, and the read-only product catalog lives in
// separate tables; so full CRUD is legitimate on this path and would not be on
// the catalog.
type Model struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Vendor      *string `json:"vendor"`
	Family      *string `json:"family"`
	Version     *string `json:"version"`
	Description *string `json:"description"`
	BestAt      *string `json:"best_at"`

	License string `json:"license"`
	Pricing string `json:"pricing"`
	Status  string `json:"status"`

	ContextWindow    *int     `json:"context_window"`
	Modalities       []string `json:"modalities"`
	ProviderEndpoint *string  `json:"provider_endpoint"`
	Tags             *string  `json:"tags"`

	// ── Computed. None of these are writable on the general update route. ──

	// Provider is the ROUTING truth — which upstream dispatch resolves against.
	// It is set by the binding route, not here.
	Provider string `json:"provider"`

	// Hosting is derived from Provider and never stored: a human-facing "where
	// does this run" label. There is no column, so nothing to drift.
	Hosting string `json:"hosting"`

	Endpoint        *string `json:"endpoint,omitempty"`
	SecretRef       *string `json:"secret_ref,omitempty"`
	LlmGatewayID    *string `json:"llm_gateway_id"`
	ProviderModelID string  `json:"provider_model_id"`

	SupportedEfforts      []string `json:"supported_efforts"`
	ToolDialect           string   `json:"tool_dialect"`
	MaxToolIterations     *int     `json:"max_tool_iterations"`
	ToolResultBudgetChars *int     `json:"tool_result_budget_chars"`

	OwnerUserID *string `json:"owner_user_id"`
	CreatedAt   string  `json:"created_at"`
	ModifiedAt  string  `json:"modified_at"`
}

// ModelCreate is the POST body. License, pricing and status take column
// defaults when empty (`proprietary` / `paid` / `available`).
type ModelCreate struct {
	Name             string   `json:"name"`
	Vendor           *string  `json:"vendor,omitempty"`
	Family           *string  `json:"family,omitempty"`
	Version          *string  `json:"version,omitempty"`
	Description      *string  `json:"description,omitempty"`
	BestAt           *string  `json:"best_at,omitempty"`
	License          string   `json:"license,omitempty"`
	Pricing          string   `json:"pricing,omitempty"`
	Status           string   `json:"status,omitempty"`
	ContextWindow    *int     `json:"context_window,omitempty"`
	Modalities       []string `json:"modalities"`
	ProviderEndpoint *string  `json:"provider_endpoint,omitempty"`
	Tags             *string  `json:"tags,omitempty"`
}

// ModelUpdate is the PATCH body. Nil preserves.
//
// The writable set matches create's exactly, and that is the service's own
// decision rather than this client's: what you could set when registering a
// model, you can change afterwards. Everything else is absent for a reason —
//
//   - provider / endpoint / llm_gateway_id / provider_model_id belong to the
//     BINDING route, which owns routing as one unit. Splitting them across two
//     routes would let a config half-move a model between gateways.
//   - secret_ref, supported_efforts, tool_dialect and the tool-loop caps are
//     platform-managed profile fields, not customer input.
//
// `name`, `license`, `pricing` and `status` are NULLIF-guarded server-side —
// they are non-null columns with CHECK constraints, so an empty string would
// fail the constraint rather than clear anything, and empty therefore preserves.
//
// Modalities is a POINTER to a slice so absent (preserve) and [] (clear) stay
// distinct in the type rather than resting on Go's nil-vs-empty-slice.
type ModelUpdate struct {
	Name             *string   `json:"name"`
	Vendor           *string   `json:"vendor"`
	Family           *string   `json:"family"`
	Version          *string   `json:"version"`
	Description      *string   `json:"description"`
	BestAt           *string   `json:"best_at"`
	License          *string   `json:"license"`
	Pricing          *string   `json:"pricing"`
	Status           *string   `json:"status"`
	ContextWindow    *int      `json:"context_window"`
	Modalities       *[]string `json:"modalities"`
	ProviderEndpoint *string   `json:"provider_endpoint"`
	Tags             *string   `json:"tags"`
}

func (c *Client) CreateModel(ctx context.Context, in ModelCreate) (*Model, error) {
	var out Model
	if err := c.Post(ctx, modelsPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetModel(ctx context.Context, id string) (*Model, error) {
	var out Model
	if err := c.Get(ctx, joinPath(modelsPath, id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateModel(ctx context.Context, id string, in ModelUpdate) (*Model, error) {
	var out Model
	if err := c.Patch(ctx, joinPath(modelsPath, id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteModel removes a registered model.
//
// It answers 409 while a live agent or Albus setting still points at the model.
// That guard lives in the application, not the database: there is NO foreign key
// on models(id), so nothing at the storage layer would stop a delete from
// leaving an agent pointing at a row that no longer exists — the failure would
// surface later, at dispatch, as a confusing routing error. Usage history does
// NOT block, because a training snapshot is meant to outlive the model it
// recorded.
func (c *Client) DeleteModel(ctx context.Context, id string) error {
	return c.Delete(ctx, joinPath(modelsPath, id))
}
