package client

import "context"

const groupsPath = "/v1/identity/humans/groups"

// Group mirrors a `human_groups` row plus its permission set.
type Group struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`

	// IsBuiltin marks a platform-created group. Builtin groups reject every
	// update and delete with 400, so one imported into Terraform can be read
	// but never managed.
	IsBuiltin bool `json:"is_builtin"`

	// SSO directory link. Both nil on local-only groups; when both are set,
	// membership is driven by the IdP on every login rather than by this API.
	ProviderID *string `json:"provider_id"`
	ExternalID *string `json:"external_id"`

	CreatedByUserID   *string `json:"created_by_user_id"`
	CreatedByUsername *string `json:"created_by_username"`
	CreatedAt         string  `json:"created_at"`
	ModifiedAt        string  `json:"modified_at"`
}

// GroupCreate is the POST body.
type GroupCreate struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// GroupUpdate is the PATCH body, and it is a THIRD update dialect — neither
// COALESCE-with-NULLIF (guardrail_policies) nor plain COALESCE (llm_gateways,
// roles). The handler branches on pointer presence and issues a separate repo
// call per field that is present:
//
//   - Name: nil skips. An empty or whitespace-only string is rejected with 400,
//     not silently ignored, so there is no way to blank it by accident.
//   - Description: nil skips; "" is written through and clears.
//   - Permissions: nil skips, and an EMPTY SLICE CLEARS ALL PERMISSIONS. Note
//     the field below deliberately has NO `,omitempty`, unlike all three of its
//     neighbours: with it, the empty slice that means "clear" would be omitted
//     and read as "leave alone", so emptying the set would silently no-op.
//     TestExpandGroupUpdate_EmptySetMarshalsAsArrayNotNull fails if it is added.
//   - SSOLink: nil leaves the link alone; a present object sets both columns at
//     once, with nil members clearing them.
//
// The handler is NOT atomic: it runs up to four repo calls, each its own
// transaction, so a failure partway through leaves earlier fields applied. A
// retried apply converges, since every call is idempotent for a given plan.
type GroupUpdate struct {
	Name        *string  `json:"name,omitempty"`
	Description *string  `json:"description,omitempty"`
	Permissions []string `json:"permissions"`
	SSOLink     *SSOLink `json:"sso_link,omitempty"`
}

// SSOLink is the nested sso_link object. Sending it with both members nil
// clears the group's directory link.
type SSOLink struct {
	ProviderID *string `json:"provider_id"`
	ExternalID *string `json:"external_id"`
}

func (c *Client) CreateGroup(ctx context.Context, in GroupCreate) (*Group, error) {
	var out Group
	if err := c.Post(ctx, groupsPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetGroup(ctx context.Context, id string) (*Group, error) {
	var out Group
	if err := c.Get(ctx, joinPath(groupsPath, id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateGroup(ctx context.Context, id string, in GroupUpdate) (*Group, error) {
	var out Group
	if err := c.Patch(ctx, joinPath(groupsPath, id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteGroup removes the group. Memberships and role attachments cascade;
// builtin groups are refused with 400.
func (c *Client) DeleteGroup(ctx context.Context, id string) error {
	return c.Delete(ctx, joinPath(groupsPath, id))
}
