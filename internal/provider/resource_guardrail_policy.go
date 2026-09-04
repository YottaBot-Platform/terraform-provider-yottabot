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
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

// ── yottabot_guardrail_policy ───────────────────────────────────────────────
//
// Two service behaviours shape this resource, and neither is guessable from
// the wire format:
//
// UPDATE PRESERVES ON ABSENCE. The service PATCHes with COALESCE, so a field
// missing from the body arrives as SQL NULL and keeps the stored value. A
// practitioner removing `description` from their config must therefore send an
// explicit empty string, or the removal is silently ignored and the plan never
// converges. clearableString does that; optionalString collapses the empty
// value back to null on read so the round trip is stable.
//
// DELETE IS A SOFT DELETE. The row is retained so audit references to it still
// resolve, and the name is released for reuse. That last part is a guarantee
// this resource depends on: without it, destroy followed by apply under the same
// name would fail on a duplicate, and a destroyed name would be unusable
// forever. The acceptance test exercises exactly that cycle.

var (
	_ resource.Resource                = (*guardrailPolicyResource)(nil)
	_ resource.ResourceWithConfigure   = (*guardrailPolicyResource)(nil)
	_ resource.ResourceWithImportState = (*guardrailPolicyResource)(nil)
)

func NewGuardrailPolicyResource() resource.Resource { return &guardrailPolicyResource{} }

type guardrailPolicyResource struct {
	client *client.Client
}

type guardrailPolicyResourceModel struct {
	ID          types.String         `tfsdk:"id"`
	Name        types.String         `tfsdk:"name"`
	Description types.String         `tfsdk:"description"`
	Definition  jsontypes.Normalized `tfsdk:"definition"`
	Tags        types.String         `tfsdk:"tags"`

	CreatedAt  types.String `tfsdk:"created_at"`
	ModifiedAt types.String `tfsdk:"modified_at"`
}

func (r *guardrailPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_guardrail_policy"
}

func (r *guardrailPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *guardrailPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A guardrail policy agents can reference. Destroying one is a soft delete on the " +
			"platform side: the row is retained so audit references to it still resolve, and the name is " +
			"released for reuse.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Policy name, unique among live policies in the account. A destroyed " +
					"policy releases its name, so the same one can be used again.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text description. Removing it from config clears it.",
			},
			"definition": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				CustomType: jsontypes.NormalizedType{},
				MarkdownDescription: "Policy definition as JSON. Free-form in v1 — a structured schema is " +
					"platform-side future work. Defaults to `{}`, and removing it from config resets it to " +
					"`{}` rather than null, because the column is non-null. Compared semantically, so key " +
					"order and whitespace do not produce diffs.",
			},
			"tags": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text tags, as the single string the current API stores (not a list).",
			},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *guardrailPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan guardrailPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := expandGuardrailPolicy(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	gp, err := r.client.CreateGuardrailPolicy(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Could not create guardrail policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenGuardrailPolicy(gp))...)
}

func (r *guardrailPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state guardrailPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	gp, err := r.client.GetGuardrailPolicy(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read guardrail policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenGuardrailPolicy(gp))...)
}

func (r *guardrailPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan guardrailPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state guardrailPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := expandGuardrailPolicy(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	gp, err := r.client.UpdateGuardrailPolicy(ctx, state.ID.ValueString(), in)
	if err != nil {
		resp.Diagnostics.AddError("Could not update guardrail policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenGuardrailPolicy(gp))...)
}

func (r *guardrailPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state guardrailPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteGuardrailPolicy(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete guardrail policy", err.Error())
	}
}

func (r *guardrailPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandGuardrailPolicy builds the body for both POST and PATCH.
//
// The service takes one struct for both and updates with COALESCE, so an
// absent field preserves rather than clears. clearableString turns a null
// attribute into a pointer to "" for exactly that reason. `definition` gets
// the same treatment with `{}` instead of "", because its column is
// NOT NULL DEFAULT '{}' — there is no null to return it to.
func expandGuardrailPolicy(m guardrailPolicyResourceModel) (client.GuardrailPolicyWrite, diag.Diagnostics) {
	var diags diag.Diagnostics
	in := client.GuardrailPolicyWrite{
		Name:        m.Name.ValueString(),
		Description: clearableString(m.Description),
		Tags:        clearableString(m.Tags),
	}

	switch {
	case m.Definition.IsUnknown():
		// Not yet known this plan — send nothing and let the service keep
		// whatever it has.
	case m.Definition.IsNull():
		in.Definition = json.RawMessage(`{}`)
	default:
		raw := m.Definition.ValueString()
		if !json.Valid([]byte(raw)) {
			diags.AddAttributeError(path.Root("definition"), "Invalid JSON",
				"definition is not valid JSON")
			return in, diags
		}
		in.Definition = json.RawMessage(raw)
	}
	return in, diags
}

func flattenGuardrailPolicy(gp *client.GuardrailPolicy) guardrailPolicyResourceModel {
	m := guardrailPolicyResourceModel{
		ID:   types.StringValue(gp.ID),
		Name: types.StringValue(gp.Name),
		// optionalString collapses "" to null, which is what makes a cleared
		// attribute round-trip: config removes it, the service stores "", and
		// state records absence rather than an empty string that would diff
		// against the removed attribute forever.
		Description: optionalString(gp.Description),
		Tags:        optionalString(gp.Tags),
		CreatedAt:   types.StringValue(gp.CreatedAt),
		ModifiedAt:  types.StringValue(gp.ModifiedAt),
	}
	// definition is Computed and can never be null in state.
	if len(gp.Definition) == 0 {
		m.Definition = jsontypes.NewNormalizedValue("{}")
	} else {
		m.Definition = jsontypes.NewNormalizedValue(string(gp.Definition))
	}
	return m
}
