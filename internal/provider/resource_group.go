package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

// ── yottabot_group ──────────────────────────────────────────────────────────
//
// A human group and its permission set. Three things about the service shape
// this resource, and none are visible from the wire format.
//
// PERMISSIONS ARE A REPLACE-SET, and the empty set is meaningful. The service
// deletes every permission row and reinserts the supplied list in one
// transaction, so sending the set is always a complete statement of intent —
// which is exactly what Terraform wants. But an ABSENT `permissions` key leaves
// the set alone while `[]` wipes it, so the empty set has to survive all the way
// to the wire. What threatens it is the struct tag rather than any logic here:
// `,omitempty` would drop exactly the empty slice that means "clear". The
// client documents that, and a test asserts on the marshalled bytes.
//
// NAME AND DESCRIPTION USE A THIRD UPDATE DIALECT. Not COALESCE — the handler
// branches on pointer presence and issues a separate repo call per present
// field. An empty name is REJECTED with 400 rather than ignored, so unlike
// every other resource here there is no way to blank it by accident.
//
// BUILTIN GROUPS ARE IMMUTABLE. `admin` is reserved at create, and any group
// with is_builtin refuses update and delete with 400. One can be imported and
// read, but never managed — Terraform will surface the service's own error on
// the first change or destroy.
//
// Group MEMBERSHIP is not managed here, for the same reason role attachments
// are absent from yottabot_role: users join groups through a separate endpoint,
// through SCIM, and through SSO's groups claim on every login. A set attribute
// here would make Terraform delete memberships the IdP had just created.

var (
	_ resource.Resource                = (*groupResource)(nil)
	_ resource.ResourceWithConfigure   = (*groupResource)(nil)
	_ resource.ResourceWithImportState = (*groupResource)(nil)
)

func NewGroupResource() resource.Resource { return &groupResource{} }

type groupResource struct {
	client *client.Client
}

type groupResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Permissions types.Set    `tfsdk:"permissions"`

	IsBuiltin  types.Bool   `tfsdk:"is_builtin"`
	ProviderID types.String `tfsdk:"sso_provider_id"`
	ExternalID types.String `tfsdk:"sso_external_id"`

	CreatedAt  types.String `tfsdk:"created_at"`
	ModifiedAt types.String `tfsdk:"modified_at"`
}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data",
			fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A human group and the permissions it grants.\n\n" +
			"Group **membership** is not managed by this resource. Users join groups through a separate " +
			"endpoint, through SCIM, and through an SSO provider's `groups` claim on every login — a " +
			"membership attribute here would delete memberships the IdP had just created.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Group name, unique within the account. `admin` is reserved and rejected. " +
					"An empty or whitespace-only name is rejected rather than ignored.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text description. Removing it from config clears it.",
			},
			"permissions": schema.SetAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Permission strings granted to members, e.g. `agents:read`. Replaced " +
					"wholesale on every update, so this set is the complete grant — a permission added " +
					"outside Terraform is removed on the next apply. An empty set means no permissions.",
			},
			"is_builtin": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "True for platform-created groups. Builtin groups reject every update " +
					"and delete, so one imported here can be read but not managed.",
			},
			"sso_provider_id": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "SSO provider linked to this group, when membership is driven by an " +
					"external directory. Read-only here — linking a group to a directory is a console and " +
					"SCIM operation.",
			},
			"sso_external_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The external directory's id for this group. Read-only here.",
			},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	perms, diags := expandPermissions(ctx, plan.Permissions)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.CreateGroup(ctx, client.GroupCreate{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
		Permissions: perms,
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not create group", err.Error())
		return
	}
	m, d := flattenGroup(ctx, g)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetGroup(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read group", err.Error())
		return
	}
	m, d := flattenGroup(ctx, g)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan groupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state groupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := expandGroupUpdate(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.UpdateGroup(ctx, state.ID.ValueString(), in)
	if err != nil {
		resp.Diagnostics.AddError("Could not update group", err.Error())
		return
	}
	m, d := flattenGroup(ctx, g)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteGroup(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete group", err.Error())
	}
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandGroupUpdate builds the PATCH body.
//
// Permissions are ALWAYS sent, and never as nil. A nil slice would be omitted
// from the body and leave the stored set alone, so emptying the set in config
// would silently do nothing. A non-nil empty slice is what clears it, and
// expandPermissions guarantees non-nil for a known value.
func expandGroupUpdate(ctx context.Context, m groupResourceModel) (client.GroupUpdate, diag.Diagnostics) {
	perms, diags := expandPermissions(ctx, m.Permissions)
	in := client.GroupUpdate{
		// Never empty: the handler 400s on a blank name, and a null here means
		// unknown rather than removed.
		Name:        nonEmptyString(m.Name),
		Description: clearableString(m.Description),
	}
	// Unknown means "not yet resolved this plan" — the one case where omitting
	// the key, and so preserving, is right.
	if !m.Permissions.IsUnknown() {
		in.Permissions = perms
	}
	return in, diags
}

// expandPermissions converts the set to a slice that is non-nil whenever the
// value is known, so an empty set encodes as `[]` and clears rather than
// encoding as `null` and preserving.
//
// The explicit []string{} on the null/unknown branch is the only part that
// does real work. ElementsAs was measured to overwrite its target with an
// allocated empty slice, so the value it returns is never nil no matter how the
// local is declared — which is why the struct tag, not this function, is where
// the preserve-vs-clear distinction can actually be lost.
func expandPermissions(ctx context.Context, set types.Set) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if set.IsNull() || set.IsUnknown() {
		return []string{}, diags
	}
	out := make([]string, 0, len(set.Elements()))
	diags.Append(set.ElementsAs(ctx, &out, false)...)
	return out, diags
}

// flattenGroup maps the API's group into the state model.
//
// Permissions come back sorted and deduplicated; a set attribute is
// order-insensitive, so that ordering never produces a diff. A group with no
// permissions flattens to an empty set rather than null, because the attribute
// is Computed and must hold a value — and because "no permissions" is a real
// state here, not an absent one.
func flattenGroup(ctx context.Context, g *client.Group) (groupResourceModel, diag.Diagnostics) {
	perms, diags := types.SetValueFrom(ctx, types.StringType, g.Permissions)
	if g.Permissions == nil {
		perms = types.SetValueMust(types.StringType, nil)
	}
	return groupResourceModel{
		ID:          types.StringValue(g.ID),
		Name:        types.StringValue(g.Name),
		Description: optionalString(&g.Description),
		Permissions: perms,
		IsBuiltin:   types.BoolValue(g.IsBuiltin),
		ProviderID:  computedString(g.ProviderID),
		ExternalID:  computedString(g.ExternalID),
		CreatedAt:   types.StringValue(g.CreatedAt),
		ModifiedAt:  types.StringValue(g.ModifiedAt),
	}, diags
}
