package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func TestGroup_TypeName(t *testing.T) {
	var resp resource.MetadataResponse
	NewGroupResource().Metadata(context.Background(),
		resource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_group" {
		t.Errorf("TypeName = %q", resp.TypeName)
	}
}

// THE test for this resource, and it is about JSON encoding rather than logic.
//
// The service distinguishes an absent `permissions` key (leave the set alone)
// from `[]` (delete every permission), so emptying the set in config produces a
// silent no-op if the key goes missing — the plan would never converge.
//
// The regression this actually catches is the struct tag. `GroupUpdate`'s other
// three fields all carry `,omitempty`, so adding it to Permissions for
// consistency is a natural edit, and it would omit exactly the empty slice that
// means "clear". Mutating the tag was verified to fail this test.
//
// It does NOT guard against a nil slice reaching the encoder: ElementsAs was
// measured to overwrite its target with an allocated empty slice, so nil is
// unreachable for a known set however the local is declared. Asserting on the
// marshalled bytes rather than the Go value is what makes the tag reachable
// here at all.
func TestExpandGroupUpdate_EmptySetMarshalsAsArrayNotNull(t *testing.T) {
	empty, d := types.SetValueFrom(context.Background(), types.StringType, []string{})
	if d.HasError() {
		t.Fatalf("build empty set: %v", d)
	}
	in, diags := expandGroupUpdate(context.Background(), groupResourceModel{
		Name:        types.StringValue("engineers"),
		Permissions: empty,
	})
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, ok := body["permissions"]
	if !ok {
		t.Fatal("permissions key is absent — the service would leave the set unchanged")
	}
	if string(got) != "[]" {
		t.Errorf("permissions = %s, want [] — null reads as absent and preserves", got)
	}
}

// The mirror image: unknown is the ONE case where omitting the key is right,
// because the value is not yet resolved and preserving is correct.
func TestExpandGroupUpdate_UnknownPermissionsOmitsTheKey(t *testing.T) {
	in, diags := expandGroupUpdate(context.Background(), groupResourceModel{
		Name:        types.StringValue("engineers"),
		Permissions: types.SetUnknown(types.StringType),
	})
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}
	if in.Permissions != nil {
		t.Errorf("permissions = %v, want nil so the key is omitted and the set preserved", in.Permissions)
	}
}

func TestExpandGroupUpdate_SendsPermissionSet(t *testing.T) {
	set, d := types.SetValueFrom(context.Background(), types.StringType,
		[]string{"agents:read", "workflows:run"})
	if d.HasError() {
		t.Fatalf("build set: %v", d)
	}
	in, diags := expandGroupUpdate(context.Background(), groupResourceModel{
		Name:        types.StringValue("engineers"),
		Permissions: set,
	})
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}
	if len(in.Permissions) != 2 {
		t.Fatalf("permissions = %v, want 2 entries", in.Permissions)
	}
}

// Name is Required and this service 400s on a blank one, so it must never be
// sent empty. Description is the opposite: removing it from config has to send
// "" or the removal is ignored.
func TestExpandGroupUpdate_NameNeverEmptyDescriptionClearable(t *testing.T) {
	in, _ := expandGroupUpdate(context.Background(), groupResourceModel{
		Name:        types.StringValue(""),
		Description: types.StringNull(),
		Permissions: types.SetNull(types.StringType),
	})
	if in.Name != nil {
		t.Errorf("name = %q — the service rejects a blank name with 400", *in.Name)
	}
	if in.Description == nil || *in.Description != "" {
		t.Errorf("description = %v, want a pointer to \"\" so the removal takes effect", in.Description)
	}
}

// A group with no permissions must flatten to an empty set, not null: the
// attribute is Computed and must hold a value after apply, and "no permissions"
// is a real state rather than an absent one.
func TestFlattenGroup_NoPermissionsIsEmptySetNotNull(t *testing.T) {
	m, diags := flattenGroup(context.Background(), &client.Group{
		ID: "id-1", Name: "engineers", Permissions: nil,
	})
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if m.Permissions.IsNull() {
		t.Error("permissions is null; a Computed attribute may not be null after apply")
	}
	if n := len(m.Permissions.Elements()); n != 0 {
		t.Errorf("permissions has %d elements, want 0", n)
	}
}

// SSO link fields are nullable on the wire and Computed here, so nil must
// become null rather than "" — an empty string would read as a real link to a
// provider with an empty id.
func TestFlattenGroup_UnlinkedSSOFieldsAreNull(t *testing.T) {
	m, _ := flattenGroup(context.Background(), &client.Group{ID: "id-1", Name: "g"})
	if !m.ProviderID.IsNull() || !m.ExternalID.IsNull() {
		t.Errorf("sso fields = %v/%v, want null for an unlinked group", m.ProviderID, m.ExternalID)
	}
}

// Membership and role attachments are deliberately absent. If a later change
// adds them as attributes, Terraform becomes the sole authority over every
// membership — deleting any the IdP or SCIM created between applies. That
// should be a decision, not a drive-by.
func TestGroupSchema_DoesNotManageMembership(t *testing.T) {
	var resp resource.SchemaResponse
	NewGroupResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	for _, name := range []string{"users", "members", "user_ids", "roles"} {
		if _, ok := resp.Schema.Attributes[name]; ok {
			t.Errorf("schema declares %q — membership is driven by SCIM and SSO, so managing it "+
				"here would delete memberships created outside Terraform", name)
		}
	}
}
