package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

// Vocabularies pinned from bot/146's CHECKs (repo.go validProviders /
// validAuthModes / validStatuses / validBudgetPolicies).
var (
	llmProviders       = []string{"anthropic", "openai", "bedrock", "vertex", "openai_compatible", "local"}
	llmAuthModes       = []string{"api_key", "iam_role", "oauth", "none"}
	llmGatewayStatuses = []string{"available", "unavailable", "draft", "deprecated"}
	llmBudgetPolicies  = []string{"warn", "require_approval", "deny", "none"}
)

// ── yottabot_llm_gateway ────────────────────────────────────────────────────
//
// THE ATTRIBUTE NAME. The wire field is `provider`, and Terraform reserves that
// as a resource meta-argument — the framework refuses a schema declaring one.
// yottabot_mcp_tool solved the same collision by calling it `vendor`, and that
// name is not available here: this row already HAS a `vendor`, meaning the
// gateway's owner/implementer rather than the upstream service. Reusing it
// would fuse two different concepts under one name in a published schema.
// So it is `upstream_provider`, decided 2026-09-03.
//
// It is also the one attribute that forces replacement, on the service's own
// authority — CreateGatewayInput accepts thirteen fields, UpdateGatewayInput
// accepts twelve, and the repo says why: "Provider is intentionally not
// updatable (changing it is a new gateway)."

var (
	_ resource.Resource                = (*llmGatewayResource)(nil)
	_ resource.ResourceWithConfigure   = (*llmGatewayResource)(nil)
	_ resource.ResourceWithImportState = (*llmGatewayResource)(nil)
)

func NewLLMGatewayResource() resource.Resource { return &llmGatewayResource{} }

type llmGatewayResource struct {
	client *client.Client
}

type llmGatewayResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	UpstreamProvider types.String `tfsdk:"upstream_provider"`
	Endpoint         types.String `tfsdk:"endpoint"`
	AuthMode         types.String `tfsdk:"auth_mode"`
	CredentialRef    types.String `tfsdk:"credential_ref"`
	Status           types.String `tfsdk:"status"`

	SupportsStreaming  types.Bool   `tfsdk:"supports_streaming"`
	SupportsToolCalls  types.Bool   `tfsdk:"supports_tool_calls"`
	SupportsJSONMode   types.Bool   `tfsdk:"supports_json_mode"`
	RateLimitPerMinute types.Int64  `tfsdk:"rate_limit_per_minute"`
	MonthlyBudgetCents types.Int64  `tfsdk:"monthly_budget_cents"`
	BudgetPolicy       types.String `tfsdk:"budget_policy"`

	Vendor           types.String `tfsdk:"vendor"`
	HealthStatus     types.String `tfsdk:"health_status"`
	LastCheckedAt    types.String `tfsdk:"last_checked_at"`
	LastError        types.String `tfsdk:"last_error"`
	ModelsCount      types.Int64  `tfsdk:"models_count"`
	LastInvokedAt    types.String `tfsdk:"last_invoked_at"`
	CatalogID        types.String `tfsdk:"catalog_id"`
	IsManagedDefault types.Bool   `tfsdk:"is_managed_default"`
	CreatedByUserID  types.String `tfsdk:"created_by_user_id"`
	CreatedAt        types.String `tfsdk:"created_at"`
	ModifiedAt       types.String `tfsdk:"modified_at"`
}

func (r *llmGatewayResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_llm_gateway"
}

func (r *llmGatewayResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *llmGatewayResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A configured route to an upstream inference provider. This is gateway " +
			"configuration only — the models reachable through it are a separate concern.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{Required: true, MarkdownDescription: "Gateway name."},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text description. Removing it from config clears it.",
			},
			"upstream_provider": schema.StringAttribute{
				Required:   true,
				Validators: []validator.String{oneOfValidator(llmProviders)},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				MarkdownDescription: "The upstream inference provider: `anthropic`, `openai`, `bedrock`, " +
					"`vertex`, `openai_compatible`, or `local`.\n\n" +
					"Named `upstream_provider` rather than `provider` because Terraform reserves " +
					"`provider` as a meta-argument, and `vendor` on this resource already means something " +
					"else — the gateway's own owner.\n\n" +
					"**Changing this forces replacement.** The API has no update path for it: a different " +
					"upstream is a different gateway.",
			},
			"endpoint": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Gateway URL. Removing it from config clears it.",
			},
			"auth_mode": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{oneOfValidator(llmAuthModes)},
				MarkdownDescription: "How the gateway authenticates upstream: `api_key`, `iam_role`, `oauth`, or `none`.",
			},
			"credential_ref": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Reference to a stored credential. A reference only — never put a " +
					"secret value here; it would land in Terraform state.",
			},
			"status": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{oneOfValidator(llmGatewayStatuses)},
				MarkdownDescription: "Lifecycle status: `draft`, `available`, `unavailable`, or `deprecated`. " +
					"Registry lifecycle, **not** operational health — see `health_status`.",
			},
			"supports_streaming": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "Whether the gateway supports streaming responses. Defaults to `true`.",
			},
			"supports_tool_calls": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "Whether the gateway supports tool calls. Defaults to `true`.",
			},
			"supports_json_mode": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "Whether the gateway supports JSON mode. Defaults to `true`.",
			},
			"rate_limit_per_minute": schema.Int64Attribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Requests per minute allowed through this gateway.",
			},
			"monthly_budget_cents": schema.Int64Attribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Monthly spend cap, in cents.",
			},
			"budget_policy": schema.StringAttribute{
				Optional: true, Computed: true,
				Validators: []validator.String{oneOfValidator(llmBudgetPolicies)},
				MarkdownDescription: "What happens when the budget is reached: `warn`, `require_approval`, " +
					"`deny`, or `none`.",
			},

			"vendor": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "The gateway's owner/implementer — `YottaBot` for the managed default. " +
					"Distinct from `upstream_provider`, which is the service it calls.",
			},
			"health_status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Operational health, computed from live checks. Separate from `status`.",
			},
			"last_checked_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last health check timestamp."},
			"last_error":      schema.StringAttribute{Computed: true, MarkdownDescription: "Last error seen, if any."},
			"models_count":    schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of models reachable through this gateway."},
			"last_invoked_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last invocation timestamp."},
			"catalog_id":      schema.StringAttribute{Computed: true, MarkdownDescription: "Catalog row this gateway was installed from, if any."},
			"is_managed_default": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether this is the Yotta-managed default gateway.",
			},
			"created_by_user_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Creator's user UUID."},
			"created_at":         schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at":        schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *llmGatewayResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan llmGatewayResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	gw, err := r.client.CreateLLMGateway(ctx, expandLLMGatewayCreate(plan))
	if err != nil {
		resp.Diagnostics.AddError("Could not create LLM gateway", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenLLMGateway(gw))...)
}

func (r *llmGatewayResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state llmGatewayResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	gw, err := r.client.GetLLMGateway(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read LLM gateway", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenLLMGateway(gw))...)
}

func (r *llmGatewayResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan llmGatewayResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state llmGatewayResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	gw, err := r.client.UpdateLLMGateway(ctx, state.ID.ValueString(), expandLLMGatewayUpdate(plan))
	if err != nil {
		resp.Diagnostics.AddError("Could not update LLM gateway", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenLLMGateway(gw))...)
}

func (r *llmGatewayResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state llmGatewayResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteLLMGateway(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete LLM gateway", err.Error())
	}
}

func (r *llmGatewayResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func expandLLMGatewayCreate(m llmGatewayResourceModel) client.LLMGatewayCreate {
	return client.LLMGatewayCreate{
		Name:               m.Name.ValueString(),
		Description:        m.Description.ValueString(),
		Provider:           m.UpstreamProvider.ValueString(),
		Endpoint:           m.Endpoint.ValueString(),
		AuthMode:           m.AuthMode.ValueString(),
		CredentialRef:      m.CredentialRef.ValueString(),
		Status:             m.Status.ValueString(),
		SupportsStreaming:  optionalBool(m.SupportsStreaming),
		SupportsToolCalls:  optionalBool(m.SupportsToolCalls),
		SupportsJSONMode:   optionalBool(m.SupportsJSONMode),
		RateLimitPerMinute: int(m.RateLimitPerMinute.ValueInt64()),
		MonthlyBudgetCents: m.MonthlyBudgetCents.ValueInt64(),
		BudgetPolicy:       m.BudgetPolicy.ValueString(),
	}
}

// expandLLMGatewayUpdate builds the PATCH body.
//
// Nil preserves, as on every COALESCE route here — so a removed optional
// attribute has to send a pointer to "" to clear it.
//
// Two fields deliberately do NOT get that treatment:
//
//   - name. This service does not guard it with NULLIF, so an empty name would
//     blank the row rather than be ignored. It is Required in the schema, so
//     the case cannot arise, and this only ever sends a real value.
//   - the vocabulary fields. auth_mode, status and budget_policy are validated
//     against a closed set, so "" is rejected as an unknown value rather than
//     read as a clear. They are Optional+Computed: a config that omits one
//     keeps whatever the server chose, which is the intended behaviour anyway.
func expandLLMGatewayUpdate(m llmGatewayResourceModel) client.LLMGatewayUpdate {
	out := client.LLMGatewayUpdate{
		Description:        clearableString(m.Description),
		Endpoint:           clearableString(m.Endpoint),
		CredentialRef:      clearableString(m.CredentialRef),
		SupportsStreaming:  optionalBool(m.SupportsStreaming),
		SupportsToolCalls:  optionalBool(m.SupportsToolCalls),
		SupportsJSONMode:   optionalBool(m.SupportsJSONMode),
		RateLimitPerMinute: optionalInt(m.RateLimitPerMinute),
		MonthlyBudgetCents: optionalInt64(m.MonthlyBudgetCents),
	}
	// Never sent empty — see nonEmptyString. `name` because this service has no
	// NULLIF guard and "" would blank the row; the other three because they are
	// validated against a closed set and "" is not in it.
	out.Name = nonEmptyString(m.Name)
	out.AuthMode = nonEmptyString(m.AuthMode)
	out.Status = nonEmptyString(m.Status)
	out.BudgetPolicy = nonEmptyString(m.BudgetPolicy)
	return out
}

func flattenLLMGateway(gw *client.LLMGateway) llmGatewayResourceModel {
	return llmGatewayResourceModel{
		ID:               types.StringValue(gw.ID),
		Name:             types.StringValue(gw.Name),
		Description:      optionalString(&gw.Description),
		UpstreamProvider: types.StringValue(gw.Provider),
		Endpoint:         optionalString(&gw.Endpoint),
		AuthMode:         types.StringValue(gw.AuthMode),
		CredentialRef:    optionalString(&gw.CredentialRef),
		Status:           types.StringValue(gw.Status),

		SupportsStreaming:  types.BoolValue(gw.SupportsStreaming),
		SupportsToolCalls:  types.BoolValue(gw.SupportsToolCalls),
		SupportsJSONMode:   types.BoolValue(gw.SupportsJSONMode),
		RateLimitPerMinute: types.Int64Value(int64(gw.RateLimitPerMinute)),
		MonthlyBudgetCents: types.Int64Value(gw.MonthlyBudgetCents),
		BudgetPolicy:       types.StringValue(gw.BudgetPolicy),

		Vendor:           computedString(gw.Vendor),
		HealthStatus:     types.StringValue(gw.HealthStatus),
		LastCheckedAt:    computedString(gw.LastCheckedAt),
		LastError:        types.StringValue(gw.LastError),
		ModelsCount:      types.Int64Value(int64(gw.ModelsCount)),
		LastInvokedAt:    computedString(gw.LastInvokedAt),
		CatalogID:        computedString(gw.CatalogID),
		IsManagedDefault: types.BoolValue(gw.IsManagedDefault),
		CreatedByUserID:  computedString(gw.CreatedByUserID),
		CreatedAt:        types.StringValue(gw.CreatedAt),
		ModifiedAt:       types.StringValue(gw.ModifiedAt),
	}
}

// optionalBool / optionalInt / optionalInt64 mirror clearableString for the
// non-string attributes, with one deliberate difference: there is no "clear" to
// express. A null bool or number has no empty value the service could store, so
// null and unknown both mean "send nothing and keep what is there".
func optionalBool(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	b := v.ValueBool()
	return &b
}

func optionalInt(v types.Int64) *int {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	i := int(v.ValueInt64())
	return &i
}

func optionalInt64(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	i := v.ValueInt64()
	return &i
}
