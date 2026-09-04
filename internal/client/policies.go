package client

import (
	"context"
	"encoding/json"
)

const policiesPath = "/v1/identity/access/policies"

// Policy mirrors an `identity_policies` row with its statements.
type Policy struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`

	// Kind is `custom` or `system`. System policies are seed-managed and refuse
	// BOTH update and delete with 400, so one imported into Terraform can be
	// read but never managed — and never destroyed. The resource does not let
	// you create one; see PolicyCreate.
	Kind string `json:"kind"`

	// Attached is a comma-separated list of role NAMES this policy is attached
	// to, computed on read. Attachments are a separate endpoint and are not
	// managed by this resource.
	Attached string `json:"attached"`

	Statements []PolicyStatement `json:"statements,omitempty"`

	CreatedAt  string `json:"created_at"`
	ModifiedAt string `json:"modified_at"`
}

// PolicyStatement is one AWS-shaped statement as it comes back from the API.
type PolicyStatement struct {
	ID        string   `json:"id"`
	Sid       string   `json:"sid"`
	Effect    string   `json:"effect"`
	Actions   []string `json:"actions"`
	Resources []string `json:"resources"`

	// Position is the evaluation order, assigned by the server from the order
	// the statements were sent. It is not settable.
	Position int `json:"position"`

	ResourceSelector json.RawMessage `json:"resource_selector,omitempty"`
}

// PolicyStatementInput is one statement on the way in. No id and no position:
// both are server-assigned, and position comes from this slice's order.
type PolicyStatementInput struct {
	Sid       string   `json:"sid,omitempty"`
	Effect    string   `json:"effect"`
	Actions   []string `json:"actions"`
	Resources []string `json:"resources"`

	ResourceSelector json.RawMessage `json:"resource_selector,omitempty"`
}

// PolicyCreate is the POST body.
//
// NO KIND FIELD, deliberately. The API accepts `kind: "system"` from any caller
// holding groups:write, and a system policy then refuses update AND delete with
// 400 — so a provider that exposed it would let a practitioner create a row
// Terraform could never change or destroy, recoverable only by direct SQL.
// Omitting the field means the server defaults to `custom`, which is the only
// kind Terraform can honestly manage.
type PolicyCreate struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Statements  []PolicyStatementInput `json:"statements"`
}

// PolicyUpdate is the PATCH body. Nil preserves.
//
// Statements is a POINTER to a slice so the three intents stay distinct in the
// type rather than resting on Go's nil-vs-empty-slice:
//
//	nil pointer            → leave the statements alone
//	pointer to empty slice → delete every statement
//	pointer to a slice     → replace the whole set
//
// Replacement is whole-set: statement ids are not stable across an update, so
// the caller sends the complete desired list. That is what Terraform has
// anyway.
//
// `name` is never sent empty — this service is the COALESCE-without-NULLIF
// dialect, so a pointer to "" would blank the row rather than be ignored.
type PolicyUpdate struct {
	Name        *string                 `json:"name"`
	Description *string                 `json:"description"`
	Statements  *[]PolicyStatementInput `json:"statements"`
}

func (c *Client) CreatePolicy(ctx context.Context, in PolicyCreate) (*Policy, error) {
	var out Policy
	if err := c.Post(ctx, policiesPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetPolicy(ctx context.Context, id string) (*Policy, error) {
	var out Policy
	if err := c.Get(ctx, joinPath(policiesPath, id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdatePolicy(ctx context.Context, id string, in PolicyUpdate) (*Policy, error) {
	var out Policy
	if err := c.Patch(ctx, joinPath(policiesPath, id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeletePolicy removes a custom policy. Its statements and its role attachments
// cascade; system policies are refused with 400.
func (c *Client) DeletePolicy(ctx context.Context, id string) error {
	return c.Delete(ctx, joinPath(policiesPath, id))
}
