package client

import (
	"context"
	"encoding/json"
)

const guardrailPoliciesPath = "/v1/agent-platform/guardrail-policies"

// GuardrailPolicy mirrors a `guardrail_policies` row — a named policy that
// agents reference. The `definition` is free-form JSON in v1; a structured DSL
// is future work on the platform side, so this provider carries it through
// unopened rather than modelling a shape that is expected to change.
type GuardrailPolicy struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description *string         `json:"description"`
	Definition  json.RawMessage `json:"definition"`
	Tags        *string         `json:"tags"`
	CreatedAt   string          `json:"created_at"`
	ModifiedAt  string          `json:"modified_at"`
}

// GuardrailPolicyWrite is both the POST and the PATCH body — the service takes
// the same struct for each.
//
// Every optional field is a pointer, and that is load-bearing on PATCH. The
// service updates with COALESCE, so a field absent from the JSON arrives as
// SQL NULL and PRESERVES the stored value; only an explicitly sent value
// overwrites it. A removed attribute therefore has to send a pointer to "" to
// clear it, exactly as yottabot_mcp_gateway does. Omitting the key means
// "leave it alone", which is the opposite of what a practitioner deleting a
// line from their config intends.
//
// Name is deliberately not clearable: the service guards it with
// NULLIF($n,”), so an empty name preserves the old one rather than blanking a
// row. The resource marks it Required, which stops the case arising.
type GuardrailPolicyWrite struct {
	Name        string          `json:"name,omitempty"`
	Description *string         `json:"description,omitempty"`
	Definition  json.RawMessage `json:"definition,omitempty"`
	Tags        *string         `json:"tags,omitempty"`
}

func (c *Client) CreateGuardrailPolicy(ctx context.Context, in GuardrailPolicyWrite) (*GuardrailPolicy, error) {
	var out GuardrailPolicy
	if err := c.Post(ctx, guardrailPoliciesPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetGuardrailPolicy(ctx context.Context, id string) (*GuardrailPolicy, error) {
	var out GuardrailPolicy
	if err := c.Get(ctx, joinPath(guardrailPoliciesPath, id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateGuardrailPolicy(ctx context.Context, id string, in GuardrailPolicyWrite) (*GuardrailPolicy, error) {
	var out GuardrailPolicy
	if err := c.Patch(ctx, joinPath(guardrailPoliciesPath, id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteGuardrailPolicy is a SOFT delete on the service side: the row keeps its
// id and gains a `deleted_at`, so audit references to a since-deleted policy
// still resolve. The name is freed for reuse — bot/268 replaced the
// unconditional UNIQUE (account_id, name) with one that applies only to live
// rows, precisely so that destroy followed by apply works in Terraform.
func (c *Client) DeleteGuardrailPolicy(ctx context.Context, id string) error {
	return c.Delete(ctx, joinPath(guardrailPoliciesPath, id))
}
