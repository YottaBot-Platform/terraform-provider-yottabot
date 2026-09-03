package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func TestMachineGroup_TypeName(t *testing.T) {
	var resp resource.MetadataResponse
	NewMachineGroupResource().Metadata(context.Background(),
		resource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_machine_group" {
		t.Errorf("TypeName = %q", resp.TypeName)
	}
}

// A typed group grants nothing directly — role and policy binding goes through
// assignments — so there is no permission set to own here. If a future change
// adds one, it is either wrong or it means the API grew a capability, and
// either way it should not happen quietly.
func TestMachineGroupSchema_HasNoPermissionsOrMembership(t *testing.T) {
	var resp resource.SchemaResponse
	NewMachineGroupResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)

	for _, name := range []string{"permissions", "members", "member_ids", "principals"} {
		if _, ok := resp.Schema.Attributes[name]; ok {
			t.Errorf("schema declares %q — a typed group carries no permission set, and membership "+
				"is a separate endpoint driven by provisioning", name)
		}
	}
	mc, ok := resp.Schema.Attributes["member_count"]
	if !ok {
		t.Fatal("member_count is missing — it is how a config observes membership without owning it")
	}
	if !mc.IsComputed() || mc.IsOptional() || mc.IsRequired() {
		t.Error("member_count must be Computed only")
	}
}

// Same pointer-presence dialect as human groups: name is rejected empty by the
// handler, description clears on "".
func TestMachineGroupUpdate_NameNeverEmptyDescriptionClearable(t *testing.T) {
	in := client.MachineGroupUpdate{
		Name:        nonEmptyString(types.StringValue("")),
		Description: clearableString(types.StringNull()),
	}
	if in.Name != nil {
		t.Errorf("name = %q — the handler rejects a blank name with 400", *in.Name)
	}
	if in.Description == nil || *in.Description != "" {
		t.Errorf("description = %v, want a pointer to \"\" so the removal takes effect", in.Description)
	}
}

// Why `,omitempty` is safe on these pointer fields and NOT on
// GroupUpdate.Permissions — the one asymmetry someone tidying the structs would
// otherwise "fix" into a bug.
//
// The handlers branch on the decoded value being nil. For a POINTER, `null` and
// an absent key both decode to nil, so the tag changes the bytes and nothing
// else. For a SLICE they diverge: `null` decodes to a nil slice (skip) while
// `[]` decodes to a non-nil empty slice (clear), so omitempty would drop
// exactly the value that means "clear".
//
// This was verified against the decode, not reasoned about: an earlier version
// of this test asserted that a nil name must be omitted rather than sent as
// null, which is a distinction the server cannot observe.
func TestOmitemptyIsLoadBearingForSlicesNotPointers(t *testing.T) {
	decodeNil := func(body, field string, target any) bool {
		if err := json.Unmarshal([]byte(body), target); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		switch v := target.(type) {
		case *struct {
			Name *string `json:"name"`
		}:
			return v.Name == nil
		case *struct {
			Permissions []string `json:"permissions"`
		}:
			return v.Permissions == nil
		}
		t.Fatalf("unhandled target for %s", field)
		return false
	}

	var p1, p2 struct {
		Name *string `json:"name"`
	}
	absent := decodeNil(`{}`, "name", &p1)
	null := decodeNil(`{"name":null}`, "name", &p2)
	if absent != null {
		t.Error("pointer: null and absent decode differently; omitempty would then be load-bearing")
	}

	var s1, s2 struct {
		Permissions []string `json:"permissions"`
	}
	sAbsent := decodeNil(`{}`, "permissions", &s1)
	sEmpty := decodeNil(`{"permissions":[]}`, "permissions", &s2)
	if sAbsent == sEmpty {
		t.Error("slice: absent and [] decode the same; the clear-vs-preserve distinction is gone, " +
			"and GroupUpdate.Permissions' missing omitempty no longer protects anything")
	}
}

func TestFlattenMachineGroup_CollapsesEmptyDescription(t *testing.T) {
	m := flattenMachineGroup(&client.MachineGroup{
		ID: "id-1", Name: "runners", Description: "", MemberCount: 4, IsBuiltin: false,
	})
	if !m.Description.IsNull() {
		t.Errorf("description = %v, want null so a cleared value round-trips", m.Description)
	}
	if m.MemberCount.ValueInt64() != 4 {
		t.Errorf("member_count = %d, want 4", m.MemberCount.ValueInt64())
	}
}
