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

// ── yottabot_machine_group ──────────────────────────────────────────────────
//
// The machine axis of the typed groups. Two things distinguish it from
// yottabot_group, and both are the API's shape rather than an omission here.
//
// NO PERMISSION SET. A typed group grants nothing directly — role and policy
// binding for these principals goes through identity_assignments, a separate
// endpoint. So there is no permissions attribute to own, unlike a human group
// where the set is the whole point.
//
// MEMBERSHIP IS PATH-PARAM, and not managed. Members are added and removed
// through POST/DELETE .../{id}/members/{principalId}, and a machine group's
// members are service accounts and robots whose lifecycle is driven by
// provisioning rather than by this config. Owning membership here would delete
// principals' group entries created by anything else between applies — the same
// reason yottabot_role does not own its attachments.
//
// Builtin groups refuse update and delete with 400, so one imported here can be
// read but never managed.

var (
	_ resource.Resource                = (*machineGroupResource)(nil)
	_ resource.ResourceWithConfigure   = (*machineGroupResource)(nil)
	_ resource.ResourceWithImportState = (*machineGroupResource)(nil)
)

func NewMachineGroupResource() resource.Resource { return &machineGroupResource{} }

type machineGroupResource struct {
	client *client.Client
}

type machineGroupResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`

	IsBuiltin   types.Bool  `tfsdk:"is_builtin"`
	MemberCount types.Int64 `tfsdk:"member_count"`

	CreatedAt  types.String `tfsdk:"created_at"`
	ModifiedAt types.String `tfsdk:"modified_at"`
}

func (r *machineGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_machine_group"
}

func (r *machineGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *machineGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A machine group — the grouping for service accounts and robots.\n\n" +
			"Unlike `yottabot_group`, a machine group grants no permissions directly: role and policy " +
			"binding for these principals goes through assignments, a separate surface. **Membership is " +
			"not managed here** either — members are added through their own endpoint, and their " +
			"lifecycle is driven by provisioning rather than by this config.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Group name, unique within the account. Trimmed server-side; an empty or " +
					"whitespace-only name is rejected rather than ignored.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text description. Removing it from config clears it.",
			},
			"is_builtin": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "True for platform-created groups. Builtin groups reject every update " +
					"and delete, so one imported here can be read but not managed.",
			},
			"member_count": schema.Int64Attribute{
				Computed: true,
				MarkdownDescription: "Number of principals in the group. Membership is managed through its " +
					"own endpoint, so this count can change without any Terraform action.",
			},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *machineGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan machineGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.CreateMachineGroup(ctx, client.MachineGroupCreate{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not create machine group", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenMachineGroup(g))...)
}

func (r *machineGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state machineGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.GetMachineGroup(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read machine group", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenMachineGroup(g))...)
}

func (r *machineGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan machineGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state machineGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := r.client.UpdateMachineGroup(ctx, state.ID.ValueString(), client.MachineGroupUpdate{
		// Never empty: the handler 400s on a blank name, and a null here means
		// unknown rather than removed.
		Name:        nonEmptyString(plan.Name),
		Description: clearableString(plan.Description),
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not update machine group", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenMachineGroup(g))...)
}

func (r *machineGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state machineGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteMachineGroup(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete machine group", err.Error())
	}
}

func (r *machineGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func flattenMachineGroup(g *client.MachineGroup) machineGroupResourceModel {
	return machineGroupResourceModel{
		ID:          types.StringValue(g.ID),
		Name:        types.StringValue(g.Name),
		Description: optionalString(&g.Description),
		IsBuiltin:   types.BoolValue(g.IsBuiltin),
		MemberCount: types.Int64Value(int64(g.MemberCount)),
		CreatedAt:   types.StringValue(g.CreatedAt),
		ModifiedAt:  types.StringValue(g.ModifiedAt),
	}
}
