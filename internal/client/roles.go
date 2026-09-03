package client

import "context"

const rolesPath = "/v1/identity/access/roles"

// Role mirrors an `identity_roles` row. The three counts are join counts the
// admin UI renders as chips; they are computed on every read and never written.
type Role struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`

	// Computed join counts. Users is DISTINCT users reachable through the
	// role's attached groups, not a direct membership count — a role has no
	// direct users.
	Users    int64 `json:"users"`
	Groups   int64 `json:"groups"`
	Policies int64 `json:"policies"`

	CreatedAt  string `json:"created_at"`
	ModifiedAt string `json:"modified_at"`
}

// RoleCreate is the POST body. Both fields are plain strings: the create path
// inserts them directly, so "" means empty, not absent.
type RoleCreate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// RoleUpdate is the PATCH body. Nil preserves.
//
// The service updates with COALESCE, so an absent field arrives as SQL NULL and
// keeps the stored value. Note this is the llm_gateways dialect, NOT the
// guardrail_policies one: there is no NULLIF guard on `name` here, so a pointer
// to "" would blank the row rather than be ignored. The resource marks name
// Required, which is what stops that ever being sent.
type RoleUpdate struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (c *Client) CreateRole(ctx context.Context, in RoleCreate) (*Role, error) {
	var out Role
	if err := c.Post(ctx, rolesPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetRole(ctx context.Context, id string) (*Role, error) {
	var out Role
	if err := c.Get(ctx, joinPath(rolesPath, id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateRole(ctx context.Context, id string, in RoleUpdate) (*Role, error) {
	var out Role
	if err := c.Patch(ctx, joinPath(rolesPath, id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteRole removes the role. Attachments to policies and groups cascade; the
// policies and groups themselves are untouched.
func (c *Client) DeleteRole(ctx context.Context, id string) error {
	return c.Delete(ctx, joinPath(rolesPath, id))
}
