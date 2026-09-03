package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

// ── yottabot_role ───────────────────────────────────────────────────────────
//
// The simplest resource in the provider: two writable strings, a hard delete,
// and no immutability rules. Worth stating what it does NOT manage, because the
// omission is deliberate rather than unfinished.
//
// A role's attached policies and groups are separate endpoints
// (POST/DELETE .../roles/{id}/policies and .../groups), and they are not
// attributes here. Modelling them as a set on this resource would make
// Terraform the sole authority on every attachment: an attachment made in the
// console, by SCIM, or by another config would show as drift and be deleted on
// the next apply. That is the same trap AWS split apart into
// aws_iam_role_policy_attachment. Attachments belong in their own resource, and
// until that exists they stay out of state rather than being half-managed.
//
// The counts are exposed read-only so a config can assert on them.

var (
	_ resource.Resource                = (*roleResource)(nil)
	_ resource.ResourceWithConfigure   = (*roleResource)(nil)
	_ resource.ResourceWithImportState = (*roleResource)(nil)
)

func NewRoleResource() resource.Resource { return &roleResource{} }

type roleResource struct {
	client *client.Client
}

type roleResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`

	Users    types.Int64 `tfsdk:"users"`
	Groups   types.Int64 `tfsdk:"groups"`
	Policies types.Int64 `tfsdk:"policies"`

	CreatedAt  types.String `tfsdk:"created_at"`
	ModifiedAt types.String `tfsdk:"modified_at"`
}

func (r *roleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (r *roleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *roleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An access role — the join point between groups and policies.\n\n" +
			"Policy and group attachments are **not** managed by this resource. They are separate API " +
			"endpoints, and modelling them here would make Terraform the sole authority over every " +
			"attachment, deleting any made outside this config on the next apply.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Role name, unique within the account.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text description. Removing it from config clears it.",
			},
			"users": schema.Int64Attribute{
				Computed: true,
				MarkdownDescription: "Distinct users reachable through this role's attached groups. A role has " +
					"no direct members, so this is always a count through groups.",
			},
			"groups": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Number of groups attached to this role.",
			},
			"policies": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Number of policies attached to this role.",
			},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan roleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	role, err := r.client.CreateRole(ctx, client.RoleCreate{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not create role", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenRole(role))...)
}

func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	role, err := r.client.GetRole(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read role", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenRole(role))...)
}

func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan roleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state roleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	role, err := r.client.UpdateRole(ctx, state.ID.ValueString(), expandRoleUpdate(plan))
	if err != nil {
		resp.Diagnostics.AddError("Could not update role", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenRole(role))...)
}

func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state roleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteRole(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete role", err.Error())
	}
}

func (r *roleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandRoleUpdate builds the PATCH body.
//
// Description is clearable — nil preserves on this COALESCE route, so a removed
// attribute has to send an explicit "". Name never is: this service has no
// NULLIF guard, so an empty name would blank the row. Name is Required, so a
// null value here means unknown rather than removed, and sending nothing is
// right in that case too.
func expandRoleUpdate(m roleResourceModel) client.RoleUpdate {
	return client.RoleUpdate{
		Name:        nonEmptyString(m.Name),
		Description: clearableString(m.Description),
	}
}

func flattenRole(role *client.Role) roleResourceModel {
	return roleResourceModel{
		ID:          types.StringValue(role.ID),
		Name:        types.StringValue(role.Name),
		Description: optionalString(&role.Description),
		Users:       types.Int64Value(role.Users),
		Groups:      types.Int64Value(role.Groups),
		Policies:    types.Int64Value(role.Policies),
		CreatedAt:   types.StringValue(role.CreatedAt),
		ModifiedAt:  types.StringValue(role.ModifiedAt),
	}
}
