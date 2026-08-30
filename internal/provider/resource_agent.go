package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	_ resource.Resource                = (*agentResource)(nil)
	_ resource.ResourceWithConfigure   = (*agentResource)(nil)
	_ resource.ResourceWithImportState = (*agentResource)(nil)
)

// agentStatuses is the settable lifecycle vocabulary (bot/019 + bot/022).
// Pinned here so a bad value fails at plan rather than as a 400 at apply.
var agentStatuses = []string{"draft", "available", "unavailable"}

// envKeyPattern is the server's own app-side check on env keys. Duplicated so
// the failure lands at plan time; the server remains the authority.
var envKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func NewAgentResource() resource.Resource { return &agentResource{} }

type agentResource struct {
	client *client.Client
}

type agentResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Status      types.String `tfsdk:"status"`

	Model        types.String `tfsdk:"model"`
	ModelID      types.String `tfsdk:"model_id"`
	SystemPrompt types.String `tfsdk:"system_prompt"`
	Tags         types.String `tfsdk:"tags"`

	ToolIDs            types.List   `tfsdk:"tool_ids"`
	DataSourceIDs      types.List   `tfsdk:"data_source_ids"`
	SecretIDs          types.List   `tfsdk:"secret_ids"`
	GuardrailPolicyIDs types.List   `tfsdk:"guardrail_policy_ids"`
	PromptID           types.String `tfsdk:"prompt_id"`
	Env                types.Map    `tfsdk:"env"`

	OrchestratorID types.String `tfsdk:"orchestrator_id"`
	UserID         types.String `tfsdk:"user_id"`
	CreatedAt      types.String `tfsdk:"created_at"`
	ModifiedAt     types.String `tfsdk:"modified_at"`
}

func (r *agentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

func (r *agentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return // provider not configured yet; framework calls again
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data",
			fmt.Sprintf("expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

func (r *agentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	idList := func(desc string) schema.ListAttribute {
		return schema.ListAttribute{
			Optional:            true,
			ElementType:         types.StringType,
			MarkdownDescription: desc,
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A YottaBot agent. Manages the agent definition; agent *runs* are not a Terraform concern.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Agent name. Not required to be unique in the tenant, which is why imports are by UUID.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text description.",
			},
			"status": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Lifecycle status: `draft`, `available`, or `unavailable`. " +
					"Defaults to `draft` server-side. Left out of config, the platform's current value is adopted — " +
					"publishing an agent from the UI does not then show as drift.",
				Validators: []validator.String{oneOfValidator(agentStatuses)},
			},
			"model": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Model name the runner reads.",
			},
			"model_id": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Model catalog UUID, when the agent is bound to one. Read-only: writes flow " +
					"through model credentials, not through this route.",
			},
			"system_prompt": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "System prompt body. Line endings are normalized to `\\n`, so a CRLF file does " +
					"not diff against an LF one.",
			},
			"tags": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Tags, as the single string the current API stores (not a list).",
			},
			"tool_ids":             idList("Tool catalog UUIDs available to the agent."),
			"data_source_ids":      idList("Data source UUIDs available to the agent."),
			"secret_ids":           idList("Secret UUIDs the runner resolves at boot. References only — no secret value is stored in state."),
			"guardrail_policy_ids": idList("Guardrail policy UUIDs applied to the agent."),
			"prompt_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Prompt UUID owning the rendered prompt body. **Removing this forces replacement**: " +
					"the API's update route cannot clear it (an empty value means \"preserve\"), so an in-place " +
					"removal would diff forever.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIfConfigured()},
			},
			"env": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Environment variables. Keys must match `^[A-Z_][A-Z0-9_]*$`.",
				Validators:          []validator.Map{envKeyValidator{}},
			},
			"orchestrator_id": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Orchestrator (Yotta Graph) UUID. Read-only: SaaS assigns it, and a value that " +
					"disagrees is rejected server-side (ADR 0009), so it is not an input.",
			},
			"user_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "UUID of the linked `kind='agent'` user minted at create.",
			},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *agentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan agentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	in, diags := expandAgent(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	agent, err := r.client.CreateAgent(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Could not create agent", err.Error())
		return
	}
	state, diags := flattenAgent(ctx, agent)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *agentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state agentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agent, err := r.client.GetAgent(ctx, state.ID.ValueString())
	if err != nil {
		// Deleted outside Terraform: drop it from state so the next plan
		// recreates it, rather than failing every plan from here on.
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read agent", err.Error())
		return
	}
	next, diags := flattenAgent(ctx, agent)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *agentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan agentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state agentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	in, diags := expandAgent(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	agent, err := r.client.UpdateAgent(ctx, state.ID.ValueString(), in)
	if err != nil {
		resp.Diagnostics.AddError("Could not update agent", err.Error())
		return
	}
	next, diags := flattenAgent(ctx, agent)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *agentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state agentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteAgent(ctx, state.ID.ValueString())
	// Already gone is the desired end state, not a failure.
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete agent", err.Error())
	}
}

func (r *agentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// By UUID. Names are not tenant-unique, so a name-based import would be
	// ambiguous — the plan rules it out for v1 deliberately.
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ── conversion ──────────────────────────────────────────────────────────────
//
// expand/flatten are plain functions over the model so they can be tested
// without a Terraform process. Everything subtle about this resource lives
// here.

// expandAgent turns planned config into a create/update body.
//
// It always sends the full desired state: empty slices and an empty map where
// config is null, because the API reads nil as "preserve" and Terraform means
// "make it so". Sending nil for an emptied list is the perpetual-diff bug the
// plan calls out by name.
func expandAgent(ctx context.Context, m agentResourceModel) (client.AgentInput, diag.Diagnostics) {
	var diags diag.Diagnostics

	in := client.AgentInput{
		Name:   m.Name.ValueString(),
		Status: m.Status.ValueString(),
	}

	// For these four, "" is the CLEAR signal server-side (COALESCE without
	// NULLIF), so a null config value must send "" rather than omit the key.
	in.Description = clearableString(m.Description)
	in.Model = clearableString(m.Model)
	in.Tags = clearableString(m.Tags)
	if p := clearableString(m.SystemPrompt); p != nil {
		normalized := normalizeNewlines(*p)
		in.SystemPrompt = &normalized
	}

	// prompt_id cannot be cleared through this route, so only a real value is
	// ever sent. Removal is handled by RequiresReplaceIfConfigured on the
	// attribute instead.
	if !m.PromptID.IsNull() && !m.PromptID.IsUnknown() && m.PromptID.ValueString() != "" {
		v := m.PromptID.ValueString()
		in.PromptID = &v
	}

	var d diag.Diagnostics
	in.ToolIDs, d = expandStringList(ctx, m.ToolIDs)
	diags.Append(d...)
	in.DataSourceIDs, d = expandStringList(ctx, m.DataSourceIDs)
	diags.Append(d...)
	in.SecretIDs, d = expandStringList(ctx, m.SecretIDs)
	diags.Append(d...)
	in.GuardrailPolicyIDs, d = expandStringList(ctx, m.GuardrailPolicyIDs)
	diags.Append(d...)
	in.Env, d = expandStringMap(ctx, m.Env)
	diags.Append(d...)

	return in, diags
}

// flattenAgent maps an API row back into state.
func flattenAgent(ctx context.Context, a *client.Agent) (agentResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	m := agentResourceModel{
		ID:             types.StringValue(a.ID),
		Name:           types.StringValue(a.Name),
		Status:         types.StringValue(a.Status),
		Description:    optionalString(a.Description),
		Model:          optionalString(a.Model),
		ModelID:        computedString(a.ModelID),
		Tags:           optionalString(a.Tags),
		PromptID:       optionalString(a.PromptID),
		OrchestratorID: computedString(a.OrchestratorID),
		UserID:         computedString(a.UserID),
		CreatedAt:      types.StringValue(a.CreatedAt),
		ModifiedAt:     types.StringValue(a.ModifiedAt),
	}

	// The prompt is normalized on the way out too. Without this, an agent
	// whose stored prompt carries CRLF would diff against an identical LF
	// config on every plan.
	if a.SystemPrompt != nil && *a.SystemPrompt != "" {
		m.SystemPrompt = types.StringValue(normalizeNewlines(*a.SystemPrompt))
	} else {
		m.SystemPrompt = types.StringNull()
	}

	var d diag.Diagnostics
	m.ToolIDs, d = flattenStringList(ctx, a.ToolIDs)
	diags.Append(d...)
	m.DataSourceIDs, d = flattenStringList(ctx, a.DataSourceIDs)
	diags.Append(d...)
	m.SecretIDs, d = flattenStringList(ctx, a.SecretIDs)
	diags.Append(d...)
	m.GuardrailPolicyIDs, d = flattenStringList(ctx, a.GuardrailPolicyIDs)
	diags.Append(d...)
	m.Env, d = flattenStringMap(ctx, a.Env)
	diags.Append(d...)

	return m, diags
}

// clearableString returns "" for a null/unknown config value, so the server
// clears the column rather than preserving it. Returns nil only when there is
// genuinely nothing to say.
func clearableString(v types.String) *string {
	if v.IsUnknown() {
		return nil
	}
	if v.IsNull() {
		empty := ""
		return &empty
	}
	s := v.ValueString()
	return &s
}

// optionalString maps a nullable API string into state, collapsing "" to null.
//
// This is what keeps an optional field from diffing forever: clearing sends "",
// the API then returns "", and config says null. Treating "" and null as the
// same absent value is what makes the round trip stable. The cost is that
// explicitly writing `description = ""` is indistinguishable from omitting it,
// which is the right trade — they mean the same thing.
func optionalString(s *string) types.String {
	if s == nil || *s == "" {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

// computedString maps a nullable API string into a Computed attribute. Computed
// attributes may not be unknown after apply, so a nil becomes null, never
// omitted.
func computedString(s *string) types.String {
	if s == nil {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

// normalizeNewlines collapses CRLF and lone CR to LF. Windows-authored prompt
// files otherwise diff against byte-identical LF content forever.
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func expandStringList(ctx context.Context, l types.List) ([]string, diag.Diagnostics) {
	// Non-nil empty slice, not nil: [] is what clears the field.
	out := []string{}
	if l.IsNull() || l.IsUnknown() {
		return out, nil
	}
	var vals []string
	diags := l.ElementsAs(ctx, &vals, false)
	if vals != nil {
		out = vals
	}
	return out, diags
}

func flattenStringList(ctx context.Context, vals []string) (types.List, diag.Diagnostics) {
	if len(vals) == 0 {
		// Null rather than an empty list, so an absent config attribute and an
		// empty server-side list agree.
		return types.ListNull(types.StringType), nil
	}
	return types.ListValueFrom(ctx, types.StringType, vals)
}

func expandStringMap(ctx context.Context, m types.Map) (map[string]string, diag.Diagnostics) {
	out := map[string]string{}
	if m.IsNull() || m.IsUnknown() {
		return out, nil
	}
	var vals map[string]string
	diags := m.ElementsAs(ctx, &vals, false)
	if vals != nil {
		out = vals
	}
	return out, diags
}

func flattenStringMap(ctx context.Context, vals map[string]string) (types.Map, diag.Diagnostics) {
	if len(vals) == 0 {
		return types.MapNull(types.StringType), nil
	}
	return types.MapValueFrom(ctx, types.StringType, vals)
}

// ── validators ──────────────────────────────────────────────────────────────

type oneOf struct{ allowed []string }

func oneOfValidator(allowed []string) validator.String { return oneOf{allowed: allowed} }

func (v oneOf) Description(context.Context) string {
	return fmt.Sprintf("value must be one of: %s", strings.Join(v.allowed, ", "))
}
func (v oneOf) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (v oneOf) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	got := req.ConfigValue.ValueString()
	for _, a := range v.allowed {
		if got == a {
			return
		}
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Invalid value",
		fmt.Sprintf("%q is not valid here; %s", got, v.Description(ctx)))
}

type envKeyValidator struct{}

func (envKeyValidator) Description(context.Context) string {
	return "env keys must match ^[A-Z_][A-Z0-9_]*$"
}
func (e envKeyValidator) MarkdownDescription(ctx context.Context) string {
	return e.Description(ctx)
}

func (envKeyValidator) ValidateMap(ctx context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for k := range req.ConfigValue.Elements() {
		if !envKeyPattern.MatchString(k) {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid environment variable name",
				fmt.Sprintf("%q must match ^[A-Z_][A-Z0-9_]*$ — the platform rejects other shapes, "+
					"and catching it here avoids a failed apply", k))
		}
	}
}

// compile-time assertions that the validators satisfy their interfaces
var (
	_ validator.String = oneOf{}
	_ validator.Map    = envKeyValidator{}
	_ attr.Value       = types.String{}
)
