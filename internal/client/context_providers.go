package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const parentHandlesPath = "/v1/context/parent-handles"

// ContextProvider mirrors a Context parent-handle row. "Context provider" is
// the Terraform/UI label for that existing resource — this provider introduces
// no new backend object, table, or lifecycle to justify the friendlier name.
type ContextProvider struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	ExternalID  string `json:"external_id"`
	DisplayName string `json:"display_name"`

	CredentialRef *string         `json:"credential_ref"`
	Discoverer    string          `json:"discoverer"`
	DiscovererCfg json.RawMessage `json:"discoverer_cfg"`

	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	PollIntervalSource  string `json:"poll_interval_source"`
	IngestionMode       string `json:"ingestion_mode"`
	State               string `json:"state"`

	LastPolledAt *string `json:"last_polled_at"`
	LastError    *string `json:"last_error"`
	CreatedAt    string  `json:"created_at"`
	ModifiedAt   string  `json:"modified_at"`
}

// ContextProviderInput is the create/update body.
//
// The UPDATE statement is a fourth shape again:
//
//	display_name / state / ingestion_mode /
//	poll_interval_source                  COALESCE(NULLIF($n,''))  → "" preserves
//	credential_ref                        COALESCE($n, col)        → "" clears
//	poll_interval_seconds                 COALESCE(NULLIF($n,0))   → 0 preserves
//	discoverer_cfg                        = $n (no COALESCE)       → always replaced
//
// type, external_id and discoverer appear in the INSERT only — the UPDATE does
// not touch them, which is what makes them replace-only in Terraform.
type ContextProviderInput struct {
	Type        string `json:"type,omitempty"`
	ExternalID  string `json:"external_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`

	CredentialRef *string         `json:"credential_ref,omitempty"`
	Discoverer    string          `json:"discoverer,omitempty"`
	DiscovererCfg json.RawMessage `json:"discoverer_cfg,omitempty"`

	PollIntervalSeconds int    `json:"poll_interval_seconds,omitempty"`
	PollIntervalSource  string `json:"poll_interval_source,omitempty"`
	IngestionMode       string `json:"ingestion_mode,omitempty"`
	State               string `json:"state,omitempty"`
}

func (c *Client) CreateContextProvider(ctx context.Context, in ContextProviderInput) (*ContextProvider, error) {
	var out ContextProvider
	if err := c.Post(ctx, parentHandlesPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetContextProvider(ctx context.Context, id string) (*ContextProvider, error) {
	var out ContextProvider
	if err := c.Get(ctx, parentHandlePath(id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateContextProvider(ctx context.Context, id string, in ContextProviderInput) (*ContextProvider, error) {
	var out ContextProvider
	if err := c.Patch(ctx, parentHandlePath(id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RetireContextProvider calls DELETE, which SOFT-retires the handle
// (state='retired'). The row survives; its discovered resources cascade-delete.
//
// This is why IsRetiredDuplicate exists — see its comment.
func (c *Client) RetireContextProvider(ctx context.Context, id string) error {
	return c.Delete(ctx, parentHandlePath(id))
}

// IsRetiredDuplicate reports the unique-constraint refusal that a create hits
// when a handle with the same (account, type, external_id) already exists.
//
// It matters far more than a normal duplicate error because DELETE only
// soft-retires: the row is still there afterwards, still holding the unique
// key. So `terraform destroy` followed by `terraform apply`, and any change to
// a replace-only field, both land here. The server maps 23505 to a 400 naming
// only the constraint, so the provider has to recognise it to say anything
// useful.
func IsRetiredDuplicate(err error) bool {
	var e *APIError
	if !asAPIError(err, &e) {
		return false
	}
	if e.Status != 400 {
		return false
	}
	msg := strings.ToLower(e.Message + " " + e.Body)
	return strings.Contains(msg, "unique constraint") &&
		strings.Contains(msg, "cx_parent_handles")
}

func parentHandlePath(id string) string {
	return fmt.Sprintf("%s/%s", parentHandlesPath, url.PathEscape(id))
}
