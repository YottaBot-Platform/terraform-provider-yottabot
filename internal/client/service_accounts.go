package client

import "context"

const serviceAccountsPath = "/v1/identity/machines/identities"

// ServiceAccount mirrors a `human_users` row of kind `service_account` — a
// non-human principal owned by a group rather than by a person.
type ServiceAccount struct {
	ID       string `json:"id"`
	Username string `json:"username"`

	// Status is `active` or `retired`. Retiring is what DELETE does; the row is
	// kept so its audit trail resolves.
	Status string `json:"status"`

	OwnerGroupID   string `json:"owner_group_id"`
	OwnerGroupName string `json:"owner_group_name"`

	CreatedAt  string `json:"created_at"`
	ModifiedAt string `json:"modified_at"`
}

// ServiceAccountCreate is the POST body.
//
// NO MINT_CREDENTIAL FIELD, deliberately. The API accepts `mint_credential:
// true` and answers with a ONE-SHOT plaintext private key. Terraform writes
// every attribute it receives into state, so exposing that would put a
// long-lived credential in the state file — in plaintext, for anyone with read
// access to the backend, forever. Credentials are minted through their own
// surface, where the plaintext is handed to a caller and never stored.
type ServiceAccountCreate struct {
	Username     string `json:"username"`
	OwnerGroupID string `json:"owner_group_id"`
}

// ServiceAccountUpdate is the PATCH body. Nil preserves.
//
// Narrow on purpose, matching the route: `status` is absent because retirement
// is DELETE (which also revokes every active credential), and a PATCH that could
// write status would let a caller un-retire an account whose credentials are
// already gone. `kind` is absent because moving a row between the human, agent
// and machine axes breaks what the typed groups and CHECK constraints assume.
type ServiceAccountUpdate struct {
	Username     *string `json:"username"`
	OwnerGroupID *string `json:"owner_group_id"`
}

func (c *Client) CreateServiceAccount(ctx context.Context, in ServiceAccountCreate) (*ServiceAccount, error) {
	var out ServiceAccount
	if err := c.Post(ctx, serviceAccountsPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetServiceAccount(ctx context.Context, id string) (*ServiceAccount, error) {
	var out ServiceAccount
	if err := c.Get(ctx, joinPath(serviceAccountsPath, id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateServiceAccount(ctx context.Context, id string, in ServiceAccountUpdate) (*ServiceAccount, error) {
	var out ServiceAccount
	if err := c.Patch(ctx, joinPath(serviceAccountsPath, id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RetireServiceAccount is what DELETE does, and the name says so: the row is
// NOT removed. Status becomes `retired` and every active credential is revoked
// in the same transaction, so the audit trail behind the principal still
// resolves.
//
// The username IS released for reuse, so `terraform destroy` followed by an
// apply of the same config succeeds. That guarantee is specific and recent:
// username uniqueness now excludes retired service accounts, and only those —
// a retired PERSON's username stays reserved.
func (c *Client) RetireServiceAccount(ctx context.Context, id string) error {
	return c.Delete(ctx, joinPath(serviceAccountsPath, id))
}
