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
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

// ── yottabot_model ──────────────────────────────────────────────────────────
//
// A model REGISTERED by this account. Not a product-catalog entry: every row on
// this path is account-scoped with an owner, and the read-only catalog lives
// elsewhere. That is what makes full CRUD legitimate here.
//
// ROUTING IS NOT MANAGED HERE. `provider`, `endpoint`, the gateway binding and
// the upstream model id belong to a separate binding route which owns them as
// one unit — a model is bound to a gateway atomically, or not at all. Exposing
// half of that pair as attributes would let a config leave a model pointing at a
// gateway with the wrong upstream id, which fails only at dispatch. They are
// exposed read-only so a config can still observe where a model routes.
//
// DESTROY CAN BE REFUSED, AND THAT IS THE POINT. There is no foreign key on
// models(id) — checked against the database rather than the migrations, because
// the migration that added `agents.model_id` calls it an "FK column" while it is
// a bare uuid. So nothing at the storage layer stops a delete from orphaning an
// agent. The service therefore answers 409 while a live agent or Albus setting
// still points at the model, and Terraform surfaces that refusal rather than
// leaving a config that plans clean and breaks at dispatch. Usage history does
// NOT block: a training snapshot is meant to outlive the model it recorded.

var (
	_ resource.Resource                = (*modelResource)(nil)
	_ resource.ResourceWithConfigure   = (*modelResource)(nil)
	_ resource.ResourceWithImportState = (*modelResource)(nil)
)

func NewModelResource() resource.Resource { return &modelResource{} }

type modelResource struct {
	client *client.Client
}

type modelResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Vendor      types.String `tfsdk:"vendor"`
	Family      types.String `tfsdk:"family"`
	Version     types.String `tfsdk:"version"`
	Description types.String `tfsdk:"description"`
	BestAt      types.String `tfsdk:"best_at"`

	License types.String `tfsdk:"license"`
	Pricing types.String `tfsdk:"pricing"`
	Status  types.String `tfsdk:"status"`

	ContextWindow    types.Int64  `tfsdk:"context_window"`
	Modalities       types.List   `tfsdk:"modalities"`
	ProviderEndpoint types.String `tfsdk:"provider_endpoint"`
	Tags             types.String `tfsdk:"tags"`

	// Routing, read-only. See the header.
	UpstreamProvider types.String `tfsdk:"upstream_provider"`
	Hosting          types.String `tfsdk:"hosting"`
	LLMGatewayID     types.String `tfsdk:"llm_gateway_id"`
	ProviderModelID  types.String `tfsdk:"provider_model_id"`

	CreatedAt  types.String `tfsdk:"created_at"`
	ModifiedAt types.String `tfsdk:"modified_at"`
}

// Vocabularies mirroring the table's CHECK constraints.
var (
	modelLicenses = []string{"open_source", "open_core", "proprietary"}
	modelPricings = []string{"free", "freemium", "paid", "self_hosted"}
	modelStatuses = []string{"available", "unavailable", "draft"}
)

func (r *modelResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_model"
}

func (r *modelResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *modelResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A model registered by your account.\n\n" +
			"**Routing is not managed here.** Which upstream a model dispatches to — and the gateway " +
			"binding that goes with it — is owned by a separate surface that sets them together, so " +
			"a config cannot leave a model half-bound. Those fields are exposed read-only.\n\n" +
			"**Destroying a model is refused while an agent still uses it** (HTTP 409). Nothing in the " +
			"database enforces that, so the check is the only thing standing between a destroy and an " +
			"agent that fails at dispatch. Past usage and training records do not block — they are " +
			"meant to outlive the model.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Model name, unique within the account.",
			},
			"vendor": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Who makes the model, e.g. `Anthropic`. Distinct from where it runs.",
			},
			"family": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Model family, e.g. `claude`.",
			},
			"version": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text version label.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text description. Removing it from config clears it.",
			},
			"best_at": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text note on what this model is good for.",
			},
			"license": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "One of `open_source`, `open_core`, `proprietary`. Defaults to `proprietary`.",
				Validators:          []validator.String{oneOfValidator(modelLicenses)},
			},
			"pricing": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "One of `free`, `freemium`, `paid`, `self_hosted`. Defaults to `paid`.",
				Validators:          []validator.String{oneOfValidator(modelPricings)},
			},
			"status": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "One of `available`, `unavailable`, `draft`. Defaults to `available`.",
				Validators:          []validator.String{oneOfValidator(modelStatuses)},
			},
			"context_window": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Context window in tokens.",
			},
			"modalities": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Supported modalities, e.g. `[\"text\"]`. An empty list clears them; " +
					"removing the attribute leaves them alone.",
			},
			"provider_endpoint": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Informational upstream endpoint label. Not the routing endpoint.",
			},
			"tags": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text tags, as the single string this API stores (not a list).",
			},
			"upstream_provider": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "The upstream this model routes to. Read-only — set together with the " +
					"gateway binding, not here. Named `upstream_provider` because `provider` is a " +
					"Terraform meta-argument.",
			},
			"hosting": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Human-facing \"where does this run\" label, derived from the upstream " +
					"provider. Not stored, so it cannot drift.",
			},
			"llm_gateway_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Gateway this model routes through, when bound. Read-only.",
			},
			"provider_model_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The upstream's own model id. Read-only.",
			},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *modelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan modelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	mods, diags := expandStringList(ctx, plan.Modalities)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.client.CreateModel(ctx, client.ModelCreate{
		Name:             plan.Name.ValueString(),
		Vendor:           optionalOut(plan.Vendor),
		Family:           optionalOut(plan.Family),
		Version:          optionalOut(plan.Version),
		Description:      optionalOut(plan.Description),
		BestAt:           optionalOut(plan.BestAt),
		License:          plan.License.ValueString(),
		Pricing:          plan.Pricing.ValueString(),
		Status:           plan.Status.ValueString(),
		ContextWindow:    optionalIntOut(plan.ContextWindow),
		Modalities:       mods,
		ProviderEndpoint: optionalOut(plan.ProviderEndpoint),
		Tags:             optionalOut(plan.Tags),
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not create model", err.Error())
		return
	}
	setModelState(ctx, m, resp.State.Set, &resp.Diagnostics)
}

func (r *modelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state modelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.client.GetModel(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read model", err.Error())
		return
	}
	setModelState(ctx, m, resp.State.Set, &resp.Diagnostics)
}

func (r *modelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state modelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := expandModelUpdate(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.client.UpdateModel(ctx, state.ID.ValueString(), in)
	if err != nil {
		resp.Diagnostics.AddError("Could not update model", err.Error())
		return
	}
	setModelState(ctx, m, resp.State.Set, &resp.Diagnostics)
}

func (r *modelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state modelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteModel(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		// A 409 here is the in-use guard, and its message names what still
		// points at the model. Surfacing it verbatim is the whole value: the
		// alternative is a destroy that succeeds and breaks an agent later.
		resp.Diagnostics.AddError("Could not delete model", err.Error())
	}
}

func (r *modelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandModelUpdate builds the PATCH body.
//
// name/license/pricing/status are NULLIF-guarded server-side — non-null columns
// with CHECK constraints, so "" would fail the constraint rather than clear
// anything. They are therefore never sent empty. The free-text fields are the
// opposite: nil preserves, so a removed attribute has to send "" to clear.
func expandModelUpdate(ctx context.Context, m modelResourceModel) (client.ModelUpdate, diag.Diagnostics) {
	in := client.ModelUpdate{
		Name:             nonEmptyString(m.Name),
		License:          nonEmptyString(m.License),
		Pricing:          nonEmptyString(m.Pricing),
		Status:           nonEmptyString(m.Status),
		Vendor:           clearableString(m.Vendor),
		Family:           clearableString(m.Family),
		Version:          clearableString(m.Version),
		Description:      clearableString(m.Description),
		BestAt:           clearableString(m.BestAt),
		ProviderEndpoint: clearableString(m.ProviderEndpoint),
		Tags:             clearableString(m.Tags),
		ContextWindow:    optionalIntOut(m.ContextWindow),
	}
	if m.Modalities.IsUnknown() {
		// Not resolved this plan — omit, and the stored list is preserved.
		return in, nil
	}
	mods, diags := expandStringList(ctx, m.Modalities)
	in.Modalities = &mods
	return in, diags
}

// optionalOut returns nil for a null/unknown value so the field is omitted, and
// a pointer otherwise. Used on CREATE, where omitting takes the column default;
// updates use clearableString instead, because there an omission preserves.
func optionalOut(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	s := v.ValueString()
	return &s
}

func optionalIntOut(v types.Int64) *int {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	i := int(v.ValueInt64())
	return &i
}

func setModelState(ctx context.Context, m *client.Model, set func(context.Context, any) diag.Diagnostics, diags *diag.Diagnostics) {
	mods, d := flattenStringList(ctx, m.Modalities)
	diags.Append(d...)
	if diags.HasError() {
		return
	}
	diags.Append(set(ctx, modelResourceModel{
		ID:               types.StringValue(m.ID),
		Name:             types.StringValue(m.Name),
		Vendor:           optionalString(m.Vendor),
		Family:           optionalString(m.Family),
		Version:          optionalString(m.Version),
		Description:      optionalString(m.Description),
		BestAt:           optionalString(m.BestAt),
		License:          types.StringValue(m.License),
		Pricing:          types.StringValue(m.Pricing),
		Status:           types.StringValue(m.Status),
		ContextWindow:    optionalIntIn(m.ContextWindow),
		Modalities:       mods,
		ProviderEndpoint: optionalString(m.ProviderEndpoint),
		Tags:             optionalString(m.Tags),
		UpstreamProvider: types.StringValue(m.Provider),
		Hosting:          types.StringValue(m.Hosting),
		LLMGatewayID:     computedString(m.LlmGatewayID),
		ProviderModelID:  types.StringValue(m.ProviderModelID),
		CreatedAt:        types.StringValue(m.CreatedAt),
		ModifiedAt:       types.StringValue(m.ModifiedAt),
	})...)
}

func optionalIntIn(i *int) types.Int64 {
	if i == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*i))
}
