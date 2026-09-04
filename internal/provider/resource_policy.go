package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
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

// ── yottabot_policy ─────────────────────────────────────────────────────────
//
// An access policy and its statements. Three things drive the design here.
//
// STATEMENTS ARE AN ORDERED LIST, NOT A SET. The server assigns each statement's
// `position` from the order it was sent, and evaluation depends on it — a deny
// short-circuits an allow. Two configs with the same statements in a different
// order are DIFFERENT policies, so a set (which is order-insensitive) would let
// Terraform silently reorder a policy into meaning something else. The list
// index IS the position, which is also why neither the statement's server id nor
// its position is exposed: both are derivable or unstable, and surfacing them
// would only create drift.
//
// KIND IS NOT SETTABLE. The API accepts `kind: "system"` from any caller holding
// groups:write, and a system policy then refuses update AND delete with 400. A
// practitioner could therefore create a row Terraform can never change or
// destroy, recoverable only by direct SQL. The create body omits the field
// entirely so the server defaults to `custom`; `kind` is Computed so an imported
// system policy still reads correctly and its own error explains itself.
//
// ROLE ATTACHMENTS ARE NOT MANAGED, for the reason yottabot_role documents:
// they are a separate endpoint, also written by the console, and owning them
// here would delete attachments made anywhere else. `attached` is exposed
// read-only so a config can still observe them.
//
// This resource requires the statements-editable PATCH. Before that, changing a
// statement meant destroying the policy — and role_policy_attachments is
// ON DELETE CASCADE, so it silently detached the policy from every role.

var (
	_ resource.Resource                = (*policyResource)(nil)
	_ resource.ResourceWithConfigure   = (*policyResource)(nil)
	_ resource.ResourceWithImportState = (*policyResource)(nil)
)

func NewPolicyResource() resource.Resource { return &policyResource{} }

type policyResource struct {
	client *client.Client
}

type policyResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Statements  types.List   `tfsdk:"statements"`

	Kind     types.String `tfsdk:"kind"`
	Attached types.String `tfsdk:"attached"`

	CreatedAt  types.String `tfsdk:"created_at"`
	ModifiedAt types.String `tfsdk:"modified_at"`
}

// policyStatementModel mirrors one nested statement. No id, no position — see
// the header.
type policyStatementModel struct {
	Sid              types.String         `tfsdk:"sid"`
	Effect           types.String         `tfsdk:"effect"`
	Actions          types.List           `tfsdk:"actions"`
	Resources        types.List           `tfsdk:"resources"`
	ResourceSelector jsontypes.Normalized `tfsdk:"resource_selector"`
}

// statementObjectTypes must mirror policyStatementModel exactly; the framework
// fails at conversion time rather than at compile time on a mismatch.
func statementObjectTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"sid":               types.StringType,
		"effect":            types.StringType,
		"actions":           types.ListType{ElemType: types.StringType},
		"resources":         types.ListType{ElemType: types.StringType},
		"resource_selector": jsontypes.NormalizedType{},
	}
}

// statementEffects mirrors the server's CHECK constraint.
var statementEffects = []string{"allow", "deny"}

func (r *policyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policy"
}

func (r *policyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *policyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An access policy and its statements.\n\n" +
			"`statements` is an **ordered list**: the server assigns each statement's evaluation " +
			"position from its order here, and a `deny` short-circuits an `allow`. Reordering the list " +
			"changes what the policy means.\n\n" +
			"Role attachments are **not** managed by this resource — they are a separate endpoint, " +
			"also written by the console, so owning them here would delete attachments made elsewhere. " +
			"`attached` exposes them read-only.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Policy name, unique within the account.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text description. Removing it from config clears it.",
			},
			"kind": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Always `custom` for a policy this provider creates. Computed rather " +
					"than configurable: the API accepts `system`, but a system policy refuses both update " +
					"and delete, so setting it would produce a resource Terraform could never change or " +
					"destroy.",
			},
			"attached": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Comma-separated names of the roles this policy is attached to. " +
					"Read-only — attachments are managed elsewhere, so this can change with no " +
					"Terraform action.",
			},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
	resp.Schema.Attributes["statements"] = schema.ListNestedAttribute{
		Optional: true,
		MarkdownDescription: "Policy statements, in evaluation order. An empty list is meaningful: it " +
			"removes every statement, leaving a policy that grants nothing.",
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"sid": schema.StringAttribute{
					Optional:            true,
					MarkdownDescription: "Optional human-readable identifier for the statement, within the policy.",
				},
				"effect": schema.StringAttribute{
					Required:            true,
					MarkdownDescription: "`allow` or `deny`. A `deny` short-circuits an earlier `allow`.",
					Validators:          []validator.String{oneOfValidator(statementEffects)},
				},
				"actions": schema.ListAttribute{
					Required:            true,
					ElementType:         types.StringType,
					MarkdownDescription: "Action patterns, e.g. `agents:read` or `agents:*`.",
				},
				"resources": schema.ListAttribute{
					Optional:            true,
					ElementType:         types.StringType,
					MarkdownDescription: "Resource patterns the actions apply to. Defaults to none.",
				},
				"resource_selector": schema.StringAttribute{
					Optional:   true,
					CustomType: jsontypes.NormalizedType{},
					MarkdownDescription: "Optional attribute-based selector (`{type, tag}`) narrowing which " +
						"resources the statement covers. Compared semantically, so key order and " +
						"whitespace do not produce diffs.",
				},
			},
		},
	}
}

func (r *policyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan policyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	stmts, diags := expandStatements(ctx, plan.Statements)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	pol, err := r.client.CreatePolicy(ctx, client.PolicyCreate{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
		Statements:  stmts,
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not create policy", err.Error())
		return
	}
	m, d := flattenPolicy(ctx, pol, plan.Statements)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *policyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state policyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	pol, err := r.client.GetPolicy(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read policy", err.Error())
		return
	}
	m, d := flattenPolicy(ctx, pol, state.Statements)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *policyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan policyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state policyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := expandPolicyUpdate(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	pol, err := r.client.UpdatePolicy(ctx, state.ID.ValueString(), in)
	if err != nil {
		resp.Diagnostics.AddError("Could not update policy", err.Error())
		return
	}
	m, d := flattenPolicy(ctx, pol, plan.Statements)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *policyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state policyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeletePolicy(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete policy", err.Error())
	}
}

func (r *policyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandPolicyUpdate builds the PATCH body.
//
// Statements are ALWAYS sent when known. The server leaves them alone when the
// field is absent, so emptying the list in config has to arrive as `[]` — a nil
// pointer would silently preserve and the plan would never converge.
func expandPolicyUpdate(ctx context.Context, m policyResourceModel) (client.PolicyUpdate, diag.Diagnostics) {
	in := client.PolicyUpdate{
		// Never empty: this service COALESCEs without a NULLIF guard, so a
		// pointer to "" would blank the row.
		Name:        nonEmptyString(m.Name),
		Description: clearableString(m.Description),
	}
	if m.Statements.IsUnknown() {
		// Not resolved this plan — the one case where omitting, and so
		// preserving, is right.
		return in, nil
	}
	stmts, diags := expandStatements(ctx, m.Statements)
	in.Statements = &stmts
	return in, diags
}

// expandStatements converts the nested list, preserving order — the order is
// the evaluation order. Always returns a non-nil slice for a known value so an
// empty list marshals as `[]` and clears.
func expandStatements(ctx context.Context, l types.List) ([]client.PolicyStatementInput, diag.Diagnostics) {
	var diags diag.Diagnostics
	out := []client.PolicyStatementInput{}
	if l.IsNull() || l.IsUnknown() {
		return out, diags
	}
	var models []policyStatementModel
	diags.Append(l.ElementsAs(ctx, &models, false)...)
	if diags.HasError() {
		return out, diags
	}
	for i, m := range models {
		// Both slices start non-nil and stay that way. `actions` and
		// `resources` are `text[] NOT NULL` server-side, and a nil slice
		// marshals to `null`, which older servers passed straight through to
		// the column as SQL NULL and answered with an opaque 500. Omitting
		// `resources` is a legitimate statement — "these actions, anywhere" —
		// so the provider states the empty set rather than relying on the
		// server to interpret an absent one.
		s := client.PolicyStatementInput{
			Sid:       m.Sid.ValueString(),
			Effect:    m.Effect.ValueString(),
			Actions:   []string{},
			Resources: []string{},
		}
		diags.Append(m.Actions.ElementsAs(ctx, &s.Actions, false)...)
		if !m.Resources.IsNull() && !m.Resources.IsUnknown() {
			diags.Append(m.Resources.ElementsAs(ctx, &s.Resources, false)...)
		}
		if !m.ResourceSelector.IsNull() && !m.ResourceSelector.IsUnknown() {
			raw := m.ResourceSelector.ValueString()
			if !json.Valid([]byte(raw)) {
				diags.AddAttributeError(
					path.Root("statements").AtListIndex(i).AtName("resource_selector"),
					"Invalid JSON", "resource_selector is not valid JSON")
				continue
			}
			s.ResourceSelector = json.RawMessage(raw)
		}
		out = append(out, s)
	}
	return out, diags
}

func flattenPolicy(ctx context.Context, pol *client.Policy, prior types.List) (policyResourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := policyResourceModel{
		ID:          types.StringValue(pol.ID),
		Name:        types.StringValue(pol.Name),
		Description: optionalString(&pol.Description),
		Kind:        types.StringValue(pol.Kind),
		Attached:    types.StringValue(pol.Attached),
		CreatedAt:   types.StringValue(pol.CreatedAt),
		ModifiedAt:  types.StringValue(pol.ModifiedAt),
	}

	models := make([]policyStatementModel, 0, len(pol.Statements))
	for _, s := range pol.Statements {
		sm := policyStatementModel{
			Sid:    optionalString(&s.Sid),
			Effect: types.StringValue(s.Effect),
		}
		actions, d := types.ListValueFrom(ctx, types.StringType, s.Actions)
		diags.Append(d...)
		sm.Actions = actions

		// An empty resources list round-trips as null, matching a config that
		// omitted the attribute entirely.
		if len(s.Resources) == 0 {
			sm.Resources = types.ListNull(types.StringType)
		} else {
			res, d := types.ListValueFrom(ctx, types.StringType, s.Resources)
			diags.Append(d...)
			sm.Resources = res
		}

		if len(s.ResourceSelector) == 0 {
			sm.ResourceSelector = jsontypes.NewNormalizedNull()
		} else {
			sm.ResourceSelector = jsontypes.NewNormalizedValue(string(s.ResourceSelector))
		}
		models = append(models, sm)
	}

	// A policy with no statements is AMBIGUOUS on the wire: the server answers
	// identically whether the config omitted `statements` entirely or set it to
	// `[]`. Terraform does not treat those as the same — null means absent,
	// `[]` means "explicitly empty" — and it rejects an apply whose result does
	// not match the config it was given.
	//
	// So the prior value breaks the tie. `prior` is the plan on create/update
	// and the previous state on read: null stays null, and anything else
	// becomes an empty list. Drift is still reported honestly — a policy whose
	// statements were deleted outside Terraform had a non-null prior, so it
	// comes back as `[]` rather than silently reading as absent.
	if len(models) == 0 {
		if prior.IsNull() {
			m.Statements = types.ListNull(types.ObjectType{AttrTypes: statementObjectTypes()})
		} else {
			empty, d := types.ListValueFrom(ctx,
				types.ObjectType{AttrTypes: statementObjectTypes()}, []policyStatementModel{})
			diags.Append(d...)
			m.Statements = empty
		}
		return m, diags
	}
	list, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: statementObjectTypes()}, models)
	diags.Append(d...)
	m.Statements = list
	return m, diags
}
