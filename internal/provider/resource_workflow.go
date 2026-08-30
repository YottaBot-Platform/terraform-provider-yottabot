package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	_ resource.Resource                     = (*workflowResource)(nil)
	_ resource.ResourceWithConfigure        = (*workflowResource)(nil)
	_ resource.ResourceWithImportState      = (*workflowResource)(nil)
	_ resource.ResourceWithConfigValidators = (*workflowResource)(nil)
)

// workflowStatuses is the settable lifecycle vocabulary (bot/022).
var workflowStatuses = []string{"draft", "available", "unavailable"}

// workflowTriggers is the CANONICAL stored vocabulary (types.go StoredTriggers).
//
// The server also accepts `schedule` / `scheduled` and normalizes them to
// `cron`, but the provider deliberately does NOT: config would say "schedule",
// the server would store "cron", and every subsequent plan would show a diff
// that applying could never resolve. Rejecting the alias at plan time with a
// message naming the replacement is the honest behaviour — the plan leaves
// accepting it optional ("if the provider accepts `schedule`…").
var workflowTriggers = []string{"manual", "cron", "webhook", "event"}

// workflowTriggerAliases are the server-side synonyms, refused here on purpose.
// Kept as data so the error message can name them precisely.
var workflowTriggerAliases = map[string]string{"schedule": "cron", "scheduled": "cron"}

func NewWorkflowResource() resource.Resource { return &workflowResource{} }

type workflowResource struct {
	client *client.Client
}

type workflowResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Status      types.String `tfsdk:"status"`

	Trigger        types.String         `tfsdk:"trigger"`
	CronSchedule   types.String         `tfsdk:"cron_schedule"`
	DefinitionJSON jsontypes.Normalized `tfsdk:"definition_json"`
	Tags           types.String         `tfsdk:"tags"`

	OrchestratorID types.String `tfsdk:"orchestrator_id"`
	LastRunAt      types.String `tfsdk:"last_run_at"`
	LastRunStatus  types.String `tfsdk:"last_run_status"`
	CreatedAt      types.String `tfsdk:"created_at"`
	ModifiedAt     types.String `tfsdk:"modified_at"`
}

func (r *workflowResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workflow"
}

func (r *workflowResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *workflowResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A YottaBot workflow definition. Terraform manages the definition; " +
			"`POST /workflows/{id}/run` is not a Terraform concern.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Workflow name. Not tenant-unique, which is why imports are by UUID.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text description.",
			},
			"status": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{oneOfValidator(workflowStatuses)},
				MarkdownDescription: "Lifecycle status: `draft`, `available`, or `unavailable`. Defaults to " +
					"`draft` server-side; left out of config, the platform's current value is adopted.",
			},
			"trigger": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{workflowTriggerValidator{}},
				MarkdownDescription: "How the workflow starts: `manual`, `cron`, `webhook`, or `event`. Defaults " +
					"to `manual`. Note `schedule` is **not** accepted here even though the YAML layer and the API " +
					"take it — it is stored as `cron`, so allowing it would diff on every plan. Write `cron`.",
			},
			"cron_schedule": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Cron expression. Required when `trigger = \"cron\"`.",
			},
			"definition_json": schema.StringAttribute{
				Required:   true,
				CustomType: jsontypes.NormalizedType{},
				MarkdownDescription: "The workflow definition as JSON — usually `jsonencode({ steps = [...] })`. " +
					"Compared semantically, so key order and whitespace do not produce diffs. " +
					"A definition carrying at least one step is validated server-side on create and update, so an " +
					"unrunnable workflow fails at apply with every problem listed, not at first run.",
			},
			"tags": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Tags, as the single string the current API stores (not a list).",
			},
			"orchestrator_id": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Orchestrator (Yotta Graph) UUID. Read-only: SaaS assigns it and a disagreeing " +
					"value is rejected server-side (ADR 0009).",
			},
			"last_run_at":     schema.StringAttribute{Computed: true, MarkdownDescription: "Timestamp of the most recent run."},
			"last_run_status": schema.StringAttribute{Computed: true, MarkdownDescription: "Status of the most recent run."},
			"created_at":      schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at":     schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

// ConfigValidators enforces the cross-field rule the schema cannot express:
// a cron trigger without a schedule is accepted by the API and then simply
// never fires, which is a worse outcome than a plan-time error.
func (r *workflowResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{cronScheduleRequired{}}
}

func (r *workflowResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan workflowResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := expandWorkflow(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	wf, err := r.client.CreateWorkflow(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Could not create workflow", err.Error())
		return
	}
	state, diags := flattenWorkflow(wf)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *workflowResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state workflowResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	wf, err := r.client.GetWorkflow(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read workflow", err.Error())
		return
	}
	next, diags := flattenWorkflow(wf)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *workflowResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan workflowResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state workflowResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := expandWorkflow(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	wf, err := r.client.UpdateWorkflow(ctx, state.ID.ValueString(), in)
	if err != nil {
		resp.Diagnostics.AddError("Could not update workflow", err.Error())
		return
	}
	next, diags := flattenWorkflow(wf)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *workflowResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state workflowResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteWorkflow(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete workflow", err.Error())
	}
}

func (r *workflowResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ── conversion ──────────────────────────────────────────────────────────────

func expandWorkflow(m workflowResourceModel) (client.WorkflowInput, diag.Diagnostics) {
	var diags diag.Diagnostics

	in := client.WorkflowInput{
		Name:    m.Name.ValueString(),
		Status:  m.Status.ValueString(),
		Trigger: canonicalTrigger(m.Trigger.ValueString()),
	}
	in.Description = clearableString(m.Description)
	in.Tags = clearableString(m.Tags)
	// Sent as "" when absent so switching away from a cron trigger actually
	// clears the stale expression instead of leaving it on the row.
	in.CronSchedule = clearableString(m.CronSchedule)

	if !m.DefinitionJSON.IsNull() && !m.DefinitionJSON.IsUnknown() {
		raw := m.DefinitionJSON.ValueString()
		if !json.Valid([]byte(raw)) {
			// jsontypes validates this too; the belt-and-braces check keeps
			// invalid JSON from reaching the jsonb cast, where the error names
			// neither the field nor the problem.
			diags.AddAttributeError(path.Root("definition_json"), "Invalid JSON",
				"definition_json is not valid JSON")
			return in, diags
		}
		in.Definition = json.RawMessage(raw)
	}
	return in, diags
}

func flattenWorkflow(w *client.Workflow) (workflowResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	m := workflowResourceModel{
		ID:             types.StringValue(w.ID),
		Name:           types.StringValue(w.Name),
		Status:         types.StringValue(w.Status),
		Description:    optionalString(w.Description),
		Tags:           optionalString(w.Tags),
		CronSchedule:   optionalString(w.CronSchedule),
		OrchestratorID: computedString(w.OrchestratorID),
		LastRunAt:      computedString(w.LastRunAt),
		LastRunStatus:  computedString(w.LastRunStatus),
		CreatedAt:      types.StringValue(w.CreatedAt),
		ModifiedAt:     types.StringValue(w.ModifiedAt),
	}

	// The server canonicalizes the trigger (schedule → cron). Storing what it
	// actually returned is what keeps state honest; the validator has already
	// stopped a practitioner writing the alias in the first place.
	m.Trigger = types.StringValue(w.Trigger)

	if len(w.Definition) == 0 {
		// A workflow created without a definition is a documented state
		// (waiting on YAML import). definition_json is Required, so represent
		// it as the empty object rather than null, which the schema forbids.
		m.DefinitionJSON = jsontypes.NewNormalizedValue("{}")
	} else {
		m.DefinitionJSON = jsontypes.NewNormalizedValue(string(w.Definition))
	}
	return m, diags
}

// canonicalTrigger lower-cases and trims, matching the server's own
// normalization. Aliases are already refused by the validator, so this only
// has to handle case and whitespace.
func canonicalTrigger(t string) string {
	return strings.ToLower(strings.TrimSpace(t))
}

// ── validators ──────────────────────────────────────────────────────────────

type workflowTriggerValidator struct{}

func (workflowTriggerValidator) Description(context.Context) string {
	return fmt.Sprintf("value must be one of: %s", strings.Join(workflowTriggers, ", "))
}
func (w workflowTriggerValidator) MarkdownDescription(ctx context.Context) string {
	return w.Description(ctx)
}

func (workflowTriggerValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	got := canonicalTrigger(req.ConfigValue.ValueString())
	for _, valid := range workflowTriggers {
		if got == valid {
			return
		}
	}
	// The alias case gets its own message: the API accepts it, so "invalid
	// value" would look like a provider bug rather than a deliberate choice.
	if canonical, ok := workflowTriggerAliases[got]; ok {
		resp.Diagnostics.AddAttributeError(req.Path, "Use the canonical trigger value",
			fmt.Sprintf("%q is accepted by the API but stored as %q, so Terraform would show a diff on every "+
				"plan that applying could never resolve. Write %q here.", got, canonical, canonical))
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Invalid trigger",
		fmt.Sprintf("%q is not a valid trigger; valid values are %s",
			got, strings.Join(workflowTriggers, ", ")))
}

// cronScheduleRequired rejects a cron trigger with no expression. The API
// accepts that combination and the workflow then simply never runs — a silent
// failure worth catching at plan time.
type cronScheduleRequired struct{}

func (cronScheduleRequired) Description(context.Context) string {
	return "cron_schedule is required when trigger is \"cron\""
}
func (c cronScheduleRequired) MarkdownDescription(ctx context.Context) string {
	return c.Description(ctx)
}

func (cronScheduleRequired) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m workflowResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateCronSchedule(m.Trigger, m.CronSchedule)...)
}

// validateCronSchedule holds the actual rule, separated from the framework
// plumbing so it can be tested directly rather than through a constructed
// tfsdk.Config.
func validateCronSchedule(trigger, cronSchedule types.String) diag.Diagnostics {
	var diags diag.Diagnostics

	if trigger.IsNull() || trigger.IsUnknown() {
		return diags
	}
	if canonicalTrigger(trigger.ValueString()) != "cron" {
		return diags
	}
	if cronSchedule.IsUnknown() {
		return diags // comes from another resource; cannot judge yet
	}
	if cronSchedule.IsNull() || strings.TrimSpace(cronSchedule.ValueString()) == "" {
		diags.AddAttributeError(path.Root("cron_schedule"), "Missing cron_schedule",
			"trigger is \"cron\" but no cron_schedule is set. The API accepts this and the workflow then "+
				"never fires, so it is refused here instead.")
	}
	return diags
}

var (
	_ validator.String         = workflowTriggerValidator{}
	_ resource.ConfigValidator = cronScheduleRequired{}
)
