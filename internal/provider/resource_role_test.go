package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func TestRole_TypeName(t *testing.T) {
	var resp resource.MetadataResponse
	NewRoleResource().Metadata(context.Background(),
		resource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_role" {
		t.Errorf("TypeName = %q", resp.TypeName)
	}
}

// This service is the llm_gateways dialect, not the guardrail_policies one:
// COALESCE with no NULLIF guard on name, so a pointer to "" blanks the row
// rather than being ignored.
func TestExpandRoleUpdate_NameNeverEmptyDescriptionClearable(t *testing.T) {
	in := expandRoleUpdate(roleResourceModel{
		Name:        types.StringValue(""),
		Description: types.StringNull(),
	})
	if in.Name != nil {
		t.Errorf("name = %q — an empty name would blank the row on this service", *in.Name)
	}
	if in.Description == nil || *in.Description != "" {
		t.Errorf("description = %v, want a pointer to \"\" so removing it from config takes effect",
			in.Description)
	}
}

func TestExpandRoleUpdate_SendsRealValues(t *testing.T) {
	in := expandRoleUpdate(roleResourceModel{
		Name:        types.StringValue("sre"),
		Description: types.StringValue("on-call"),
	})
	if in.Name == nil || *in.Name != "sre" {
		t.Errorf("name = %v, want sre", in.Name)
	}
	if in.Description == nil || *in.Description != "on-call" {
		t.Errorf("description = %v, want on-call", in.Description)
	}
}

func TestFlattenRole_CollapsesEmptyDescriptionAndKeepsCounts(t *testing.T) {
	m := flattenRole(&client.Role{
		ID: "id-1", Name: "sre", Description: "",
		Users: 7, Groups: 2, Policies: 3,
	})
	if !m.Description.IsNull() {
		t.Errorf("description = %v, want null so a cleared value round-trips", m.Description)
	}
	if m.Users.ValueInt64() != 7 || m.Groups.ValueInt64() != 2 || m.Policies.ValueInt64() != 3 {
		t.Errorf("counts = %d/%d/%d, want 7/2/3",
			m.Users.ValueInt64(), m.Groups.ValueInt64(), m.Policies.ValueInt64())
	}
}

// Attachments are deliberately not attributes. Adding them would make
// Terraform the sole authority on every attachment, deleting any made in the
// console or by another config on the next apply — the trap AWS split into
// aws_iam_role_policy_attachment. This should be a decision, not a drive-by.
func TestRoleSchema_DoesNotManageAttachments(t *testing.T) {
	var resp resource.SchemaResponse
	NewRoleResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	for _, name := range []string{"policy_ids", "group_ids", "attached_policies", "attached_groups"} {
		if _, ok := resp.Schema.Attributes[name]; ok {
			t.Errorf("schema declares %q — attachments belong in their own resource", name)
		}
	}
	// The read-only counts must stay readable, though: a config asserting on
	// them is the supported way to check attachments without owning them.
	for _, name := range []string{"users", "groups", "policies"} {
		a, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Fatalf("count attribute %q is missing", name)
		}
		if !a.IsComputed() || a.IsOptional() || a.IsRequired() {
			t.Errorf("%q must be Computed only — it is a join count, not an input", name)
		}
	}
}

// nonEmptyString backs `name` on three resources now. Null and unknown must
// both preserve, and only a real value is sent.
func TestNonEmptyString(t *testing.T) {
	for name, v := range map[string]types.String{
		"null":    types.StringNull(),
		"unknown": types.StringUnknown(),
		"empty":   types.StringValue(""),
	} {
		if got := nonEmptyString(v); got != nil {
			t.Errorf("%s = %q, want nil", name, *got)
		}
	}
	if got := nonEmptyString(types.StringValue("sre")); got == nil || *got != "sre" {
		t.Errorf("value = %v, want sre", got)
	}
}
