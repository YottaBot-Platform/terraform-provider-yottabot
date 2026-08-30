package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
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

var (
	_ resource.Resource                     = (*contextProviderResource)(nil)
	_ resource.ResourceWithConfigure        = (*contextProviderResource)(nil)
	_ resource.ResourceWithImportState      = (*contextProviderResource)(nil)
	_ resource.ResourceWithConfigValidators = (*contextProviderResource)(nil)
)

// Vocabularies pinned from the live API (cx_handlers.go validators).
var (
	contextProviderStates   = []string{"active", "paused", "retired"}
	contextIngestionModes   = []string{"poll", "hybrid", "stream"}
	contextPollSources      = []string{"default", "override"}
	pollSourceDefaultString = "default"
)

func NewContextProviderResource() resource.Resource { return &contextProviderResource{} }

type contextProviderResource struct {
	client *client.Client
}

type contextProviderResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Type        types.String `tfsdk:"type"`
	ExternalID  types.String `tfsdk:"external_id"`
	DisplayName types.String `tfsdk:"display_name"`

	CredentialRef     types.String         `tfsdk:"credential_ref"`
	Discoverer        types.String         `tfsdk:"discoverer"`
	DiscovererCfgJSON jsontypes.Normalized `tfsdk:"discoverer_cfg_json"`

	PollIntervalSeconds types.Int64  `tfsdk:"poll_interval_seconds"`
	PollIntervalSource  types.String `tfsdk:"poll_interval_source"`
	IngestionMode       types.String `tfsdk:"ingestion_mode"`
	State               types.String `tfsdk:"state"`

	LastPolledAt types.String `tfsdk:"last_polled_at"`
	LastError    types.String `tfsdk:"last_error"`
	CreatedAt    types.String `tfsdk:"created_at"`
	ModifiedAt   types.String `tfsdk:"modified_at"`
}

func (r *contextProviderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_context_provider"
}

func (r *contextProviderResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *contextProviderResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	// type / external_id / discoverer appear only in the INSERT — the UPDATE
	// statement does not touch them — so changing any of them must replace.
	replaceOnly := []planmodifier.String{stringplanmodifier.RequiresReplace()}

	resp.Schema = schema.Schema{
		MarkdownDescription: "A Context provider registration — the parent handle a discoverer polls under. " +
			"Registers the handle; it does not poll it or assert on discovered resources.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"type": schema.StringAttribute{
				Required:            true,
				PlanModifiers:       replaceOnly,
				MarkdownDescription: "Provider type, e.g. `github_org`. Replace-only: the update route cannot change it.",
			},
			"external_id": schema.StringAttribute{
				Required:      true,
				PlanModifiers: replaceOnly,
				MarkdownDescription: "Identifier of the thing out there, e.g. the GitHub org name. " +
					"Replace-only. Unique per account with `type`.",
			},
			"display_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Human-readable name.",
			},
			"discoverer": schema.StringAttribute{
				Required:      true,
				PlanModifiers: replaceOnly,
				MarkdownDescription: "Discoverer implementation, e.g. `github`, `eks`. Replace-only — and note the " +
					"caveat under **Recreating a retired provider** in the provider README before changing it.",
			},
			"credential_ref": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Reference to the credential the discoverer uses. A reference, never a secret " +
					"value — nothing sensitive is copied into Terraform state.",
			},
			"discoverer_cfg_json": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				CustomType: jsontypes.NormalizedType{},
				MarkdownDescription: "Discoverer configuration as JSON. Defaults to `{}`. Compared semantically, " +
					"so key order and whitespace do not produce diffs.",
			},
			"poll_interval_seconds": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Discovery cadence. Server-defaulted per discoverer and ingestion mode, and " +
					"clamped up to a per-provider floor — so omit it unless you mean to override, and expect the " +
					"server's value to differ from a below-floor request. Setting it makes " +
					"`poll_interval_source` `override`.",
			},
			"poll_interval_source": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{oneOfValidator(contextPollSources)},
				MarkdownDescription: "`default` inherits the account cadence; `override` uses " +
					"`poll_interval_seconds`. Setting `default` makes the server IGNORE any " +
					"`poll_interval_seconds`, so the two cannot be combined.",
			},
			"ingestion_mode": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{oneOfValidator(contextIngestionModes)},
				MarkdownDescription: "How freshness is maintained: `poll`, `hybrid`, or `stream`. Server-defaulted " +
					"per discoverer (k8s defaults to `hybrid`).",
			},
			"state": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{oneOfValidator(contextProviderStates)},
				MarkdownDescription: "`active`, `paused`, or `retired`. Defaults to `active`. " +
					"`terraform destroy` sets `retired` — it does not remove the row.",
			},
			"last_polled_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Timestamp of the last successful discovery."},
			"last_error":     schema.StringAttribute{Computed: true, MarkdownDescription: "Last discovery error, if any."},
			"created_at":     schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at":    schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *contextProviderResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{pollSourceConflict{}}
}

func (r *contextProviderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan contextProviderResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := expandContextProvider(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	cp, err := r.client.CreateContextProvider(ctx, in)
	if err != nil {
		if client.IsRetiredDuplicate(err) {
			// The single most likely failure, and the least self-explanatory:
			// the server's message names only a constraint.
			resp.Diagnostics.AddError("A Context provider with this type and external_id already exists",
				fmt.Sprintf("type=%q external_id=%q is already registered in this account.\n\n"+
					"Deleting a Context provider only SOFT-retires it (state='retired'); the row survives and keeps "+
					"holding the unique key. So this also happens after `terraform destroy`, and after changing a "+
					"replace-only field (type, external_id, discoverer).\n\n"+
					"Recovery: import the existing row and set `state = \"active\"`:\n"+
					"  terraform import %s <provider-uuid>\n\n"+
					"Server said: %v",
					in.Type, in.ExternalID, req.Config.Raw.String(), err))
			return
		}
		resp.Diagnostics.AddError("Could not create Context provider", err.Error())
		return
	}

	state, diags := flattenContextProvider(cp)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *contextProviderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state contextProviderResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	cp, err := r.client.GetContextProvider(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read Context provider", err.Error())
		return
	}
	next, diags := flattenContextProvider(cp)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *contextProviderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan contextProviderResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state contextProviderResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := expandContextProvider(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Replace-only fields are never patched: the route ignores them, and
	// sending them would imply an update that silently did not happen.
	in.Type, in.ExternalID, in.Discoverer = "", "", ""

	cp, err := r.client.UpdateContextProvider(ctx, state.ID.ValueString(), in)
	if err != nil {
		resp.Diagnostics.AddError("Could not update Context provider", err.Error())
		return
	}
	next, diags := flattenContextProvider(cp)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

// Delete soft-retires. The resource leaves Terraform state, but the row stays
// in the database holding its unique key — see IsRetiredDuplicate.
func (r *contextProviderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state contextProviderResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.RetireContextProvider(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not retire Context provider", err.Error())
		return
	}
	resp.Diagnostics.AddWarning("Context provider retired, not deleted",
		"The DELETE route soft-retires the handle (state='retired'); the row remains and keeps holding "+
			"(type, external_id) unique for this account. Re-creating the same pair will fail until that row is "+
			"imported and reactivated.")
}

func (r *contextProviderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ── conversion ──────────────────────────────────────────────────────────────

func expandContextProvider(m contextProviderResourceModel) (client.ContextProviderInput, diag.Diagnostics) {
	var diags diag.Diagnostics

	in := client.ContextProviderInput{
		Type:               m.Type.ValueString(),
		ExternalID:         m.ExternalID.ValueString(),
		DisplayName:        m.DisplayName.ValueString(),
		Discoverer:         m.Discoverer.ValueString(),
		PollIntervalSource: m.PollIntervalSource.ValueString(),
		IngestionMode:      m.IngestionMode.ValueString(),
		State:              m.State.ValueString(),
	}
	// credential_ref is COALESCE($n, col): "" clears, omission preserves.
	in.CredentialRef = clearableString(m.CredentialRef)

	if !m.PollIntervalSeconds.IsNull() && !m.PollIntervalSeconds.IsUnknown() {
		in.PollIntervalSeconds = int(m.PollIntervalSeconds.ValueInt64())
	}

	if !m.DiscovererCfgJSON.IsNull() && !m.DiscovererCfgJSON.IsUnknown() {
		raw := m.DiscovererCfgJSON.ValueString()
		if !json.Valid([]byte(raw)) {
			diags.AddAttributeError(path.Root("discoverer_cfg_json"), "Invalid JSON",
				"discoverer_cfg_json is not valid JSON")
			return in, diags
		}
		in.DiscovererCfg = json.RawMessage(raw)
	}
	return in, diags
}

func flattenContextProvider(cp *client.ContextProvider) (contextProviderResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	m := contextProviderResourceModel{
		ID:                  types.StringValue(cp.ID),
		Type:                types.StringValue(cp.Type),
		ExternalID:          types.StringValue(cp.ExternalID),
		DisplayName:         types.StringValue(cp.DisplayName),
		CredentialRef:       optionalString(cp.CredentialRef),
		Discoverer:          types.StringValue(cp.Discoverer),
		PollIntervalSeconds: types.Int64Value(int64(cp.PollIntervalSeconds)),
		PollIntervalSource:  types.StringValue(cp.PollIntervalSource),
		IngestionMode:       types.StringValue(cp.IngestionMode),
		State:               types.StringValue(cp.State),
		LastPolledAt:        computedString(cp.LastPolledAt),
		LastError:           computedString(cp.LastError),
		CreatedAt:           types.StringValue(cp.CreatedAt),
		ModifiedAt:          types.StringValue(cp.ModifiedAt),
	}

	// discoverer_cfg_json is Computed, so it can never be null in state.
	if len(cp.DiscovererCfg) == 0 {
		m.DiscovererCfgJSON = jsontypes.NewNormalizedValue("{}")
	} else {
		m.DiscovererCfgJSON = jsontypes.NewNormalizedValue(string(cp.DiscovererCfg))
	}
	return m, diags
}

// ── validators ──────────────────────────────────────────────────────────────

// pollSourceConflict refuses `poll_interval_source = "default"` combined with
// an explicit `poll_interval_seconds`.
//
// The server does not error on that combination — it silently IGNORES the
// seconds and resets the handle to the account default. Config would then say
// one number, the row would hold another, and no apply could reconcile them.
type pollSourceConflict struct{}

func (pollSourceConflict) Description(context.Context) string {
	return "poll_interval_seconds cannot be set when poll_interval_source is \"default\""
}
func (p pollSourceConflict) MarkdownDescription(ctx context.Context) string {
	return p.Description(ctx)
}

func (pollSourceConflict) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m contextProviderResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validatePollSource(m.PollIntervalSource, m.PollIntervalSeconds)...)
}

// validatePollSource holds the rule, split out so it is directly testable.
func validatePollSource(source types.String, seconds types.Int64) diag.Diagnostics {
	var diags diag.Diagnostics

	if source.IsNull() || source.IsUnknown() {
		return diags
	}
	if source.ValueString() != pollSourceDefaultString {
		return diags
	}
	if seconds.IsNull() || seconds.IsUnknown() {
		return diags
	}
	diags.AddAttributeError(path.Root("poll_interval_seconds"), "Conflicting cadence configuration",
		"poll_interval_source is \"default\", which makes the server inherit the account cadence and IGNORE "+
			"poll_interval_seconds. The value here would never be stored, so every plan would show a diff. "+
			"Either drop poll_interval_seconds, or set poll_interval_source = \"override\".")
	return diags
}

var _ resource.ConfigValidator = pollSourceConflict{}
