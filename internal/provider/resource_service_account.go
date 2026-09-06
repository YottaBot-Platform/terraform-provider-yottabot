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

// ── yottabot_service_account ────────────────────────────────────────────────
//
// A non-human principal owned by a group. Three things are worth stating,
// because each is a decision rather than a mapping.
//
// NO CREDENTIAL ATTRIBUTE. The API can mint a keypair at create time and answer
// with the plaintext private key, once. Terraform writes every attribute it
// receives into state, so surfacing that would put a long-lived credential in
// the state file in plaintext, for anyone with read access to the backend,
// forever. Credentials are minted through their own surface instead, where the
// plaintext is handed to a caller and never stored. This resource manages the
// identity; it does not hand out its keys.
//
// DESTROY RETIRES, IT DOES NOT DELETE. The row is kept — status becomes
// `retired` and every active credential is revoked in the same transaction — so
// the audit trail behind the principal still resolves. `terraform destroy`
// therefore leaves something behind, deliberately, and `status` exposes it.
//
// THE USERNAME IS RELEASED ON RETIRE, so destroy → apply of the same config
// works. That guarantee is recent and narrow: username uniqueness excludes
// retired service accounts, and only those. A retired PERSON's username stays
// reserved, which is a different question with a different answer.

var (
	_ resource.Resource                = (*serviceAccountResource)(nil)
	_ resource.ResourceWithConfigure   = (*serviceAccountResource)(nil)
	_ resource.ResourceWithImportState = (*serviceAccountResource)(nil)
)

func NewServiceAccountResource() resource.Resource { return &serviceAccountResource{} }

type serviceAccountResource struct {
	client *client.Client
}

type serviceAccountResourceModel struct {
	ID           types.String `tfsdk:"id"`
	Username     types.String `tfsdk:"username"`
	OwnerGroupID types.String `tfsdk:"owner_group_id"`

	OwnerGroupName types.String `tfsdk:"owner_group_name"`
	Status         types.String `tfsdk:"status"`

	CreatedAt  types.String `tfsdk:"created_at"`
	ModifiedAt types.String `tfsdk:"modified_at"`
}

func (r *serviceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

func (r *serviceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *serviceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A service account — a non-human principal owned by a group.\n\n" +
			"**This resource does not hand out credentials.** The API can mint a keypair and return " +
			"the private key once, but Terraform writes everything it receives into state, so that " +
			"would leave a long-lived credential in the state file in plaintext. Mint credentials " +
			"through their own surface.\n\n" +
			"**`terraform destroy` retires rather than deletes.** The row is kept and its credentials " +
			"are revoked, so the audit trail still resolves — `status` becomes `retired`. The username " +
			"is released, so re-applying the same config afterwards works.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"username": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Login handle, unique among live principals in the account. " +
					"A retired service account releases its username for reuse.",
			},
			"owner_group_id": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "UUID of the group that owns this account. A group in another " +
					"tenant is reported as not found rather than forbidden, so the route never " +
					"confirms another tenant's group exists.",
			},
			"owner_group_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the owning group, resolved on read.",
			},
			"status": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "`active`, or `retired` after a destroy. Not settable: retirement " +
					"is the destroy path and also revokes credentials, so a writable status could " +
					"produce a live-looking identity that cannot authenticate.",
			},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *serviceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sa, err := r.client.CreateServiceAccount(ctx, client.ServiceAccountCreate{
		Username:     plan.Username.ValueString(),
		OwnerGroupID: plan.OwnerGroupID.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not create service account", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenServiceAccount(sa))...)
}

func (r *serviceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sa, err := r.client.GetServiceAccount(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read service account", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenServiceAccount(sa))...)
}

func (r *serviceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sa, err := r.client.UpdateServiceAccount(ctx, state.ID.ValueString(), client.ServiceAccountUpdate{
		// Both are Required, so a null here means unknown rather than removed,
		// and neither may be sent empty: an empty username would fail the
		// service's own validation, and an empty group would orphan ownership.
		Username:     nonEmptyString(plan.Username),
		OwnerGroupID: nonEmptyString(plan.OwnerGroupID),
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not update service account", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenServiceAccount(sa))...)
}

// Delete RETIRES the account — see the header. The row survives on purpose, so
// this is one of the few resources where destroy leaves something behind.
func (r *serviceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.RetireServiceAccount(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not retire service account", err.Error())
	}
}

func (r *serviceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func flattenServiceAccount(sa *client.ServiceAccount) serviceAccountResourceModel {
	return serviceAccountResourceModel{
		ID:             types.StringValue(sa.ID),
		Username:       types.StringValue(sa.Username),
		OwnerGroupID:   types.StringValue(sa.OwnerGroupID),
		OwnerGroupName: types.StringValue(sa.OwnerGroupName),
		Status:         types.StringValue(sa.Status),
		CreatedAt:      types.StringValue(sa.CreatedAt),
		ModifiedAt:     types.StringValue(sa.ModifiedAt),
	}
}
