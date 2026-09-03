package client

import "context"

const machineGroupsPath = "/v1/identity/machines/groups"

// MachineGroup mirrors a `machine_groups` row — the machine axis of the typed
// groups (the other is agent groups, which the provider does not expose).
//
// Unlike a human group, a typed group carries NO permission set: role and
// policy binding for these happens through identity_assignments, a separate
// endpoint. So there is no permissions field to manage here, and its absence is
// the API's shape rather than an omission.
type MachineGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`

	// IsBuiltin marks a platform-created group; those refuse update and delete
	// with 400, so one imported into Terraform can be read but never managed.
	IsBuiltin bool `json:"is_builtin"`

	// MemberCount is computed. Membership is managed through
	// POST/DELETE .../{id}/members/{principalId}, not through this row.
	MemberCount int `json:"member_count"`

	CreatedAt  string `json:"created_at"`
	ModifiedAt string `json:"modified_at"`
}

// MachineGroupCreate is the POST body. Name is trimmed server-side and an empty
// one is rejected with 400.
type MachineGroupCreate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// MachineGroupUpdate is the PATCH body, in the human-groups dialect rather than
// either COALESCE variant: the handler branches on pointer presence and issues a
// separate UPDATE per field that is present.
//
//   - Name: nil skips. An empty or whitespace-only string is rejected with 400
//     rather than silently ignored, so it cannot be blanked by accident.
//   - Description: nil skips; "" is written through and clears.
//
// Like human groups this is NOT atomic — two statements, each its own
// transaction — so a failure between them leaves the first applied. A retried
// apply converges.
type MachineGroupUpdate struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

func (c *Client) CreateMachineGroup(ctx context.Context, in MachineGroupCreate) (*MachineGroup, error) {
	var out MachineGroup
	if err := c.Post(ctx, machineGroupsPath, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetMachineGroup(ctx context.Context, id string) (*MachineGroup, error) {
	var out MachineGroup
	if err := c.Get(ctx, joinPath(machineGroupsPath, id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateMachineGroup(ctx context.Context, id string, in MachineGroupUpdate) (*MachineGroup, error) {
	var out MachineGroup
	if err := c.Patch(ctx, joinPath(machineGroupsPath, id), in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteMachineGroup removes the group. Builtin groups are refused with 400.
func (c *Client) DeleteMachineGroup(ctx context.Context, id string) error {
	return c.Delete(ctx, joinPath(machineGroupsPath, id))
}
