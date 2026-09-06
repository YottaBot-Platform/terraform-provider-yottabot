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
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

// ── yottabot_prompt ─────────────────────────────────────────────────────────
//
// A prompt is a CONTAINER; its content lives in immutable versions. That single
// fact drives everything here.
//
// EDITING A BODY PUBLISHES A NEW VERSION. Versions are immutable once published,
// because editing one silently changes what every agent already referencing that
// version executes. So `version` is required alongside `body`, and it is
// supplied rather than derived: bumping major/minor/patch is the author's
// decision, and a provider that guessed would eventually guess wrong in the
// direction that hides a breaking change.
//
// THE CONTENT FIELDS ARE SENT ONLY WHEN THEY CHANGE. This is the part that is
// easy to get wrong. Terraform calls Update when ANY attribute changes, so an
// edit to `description` alone would otherwise re-send the unchanged version and
// ask the service to publish it again — and `UNIQUE (prompt_id, version)` turns
// that into a duplicate-key error on an update that never touched the content.
// expandPromptUpdate compares against prior state and omits the three content
// fields when they are unchanged.
//
// Practically: change `body`, and you must bump `version` in the same apply.
// Terraform will tell you so with the service's own error rather than silently
// publishing over a version other agents are pinned to.

var (
	_ resource.Resource                = (*promptResource)(nil)
	_ resource.ResourceWithConfigure   = (*promptResource)(nil)
	_ resource.ResourceWithImportState = (*promptResource)(nil)
)

func NewPromptResource() resource.Resource { return &promptResource{} }

type promptResource struct {
	client *client.Client
}

type promptResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Version     types.String `tfsdk:"version"`
	Body        types.String `tfsdk:"body"`
	Variables   types.List   `tfsdk:"variables"`

	UsedBy types.Int64 `tfsdk:"used_by"`

	CreatedAt  types.String `tfsdk:"created_at"`
	ModifiedAt types.String `tfsdk:"modified_at"`
}

func (r *promptResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_prompt"
}

func (r *promptResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *promptResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A prompt and its latest published version.\n\n" +
			"Prompt versions are **immutable once published** — editing one would change what every " +
			"agent already referencing it executes. So changing `body` or `variables` **publishes a " +
			"new version**, and you must bump `version` in the same apply. The version is yours to " +
			"choose: a provider that guessed the bump would eventually guess wrong in the direction " +
			"that hides a breaking change.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Prompt name, unique within the account.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text description. Edited in place; removing it clears it.",
			},
			"version": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Semver of the published version, e.g. `1.2.0` — no leading `v`. " +
					"Bump it whenever `body` or `variables` changes; re-publishing an existing version " +
					"is refused.",
			},
			"body": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The prompt template. Changing this publishes a new version.",
			},
			"variables": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Declared input names for the template. Part of the version's " +
					"contract, so changing them publishes a new version too.",
			},
			"used_by": schema.Int64Attribute{
				Computed: true,
				MarkdownDescription: "Number of agents referencing this prompt. Changes without any " +
					"Terraform action.",
			},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *promptResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan promptResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	vars, diags := expandStringList(ctx, plan.Variables)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.CreatePrompt(ctx, client.PromptCreate{
		Name:           plan.Name.ValueString(),
		Description:    plan.Description.ValueString(),
		InitialVersion: plan.Version.ValueString(),
		Body:           plan.Body.ValueString(),
		Variables:      vars,
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not create prompt", err.Error())
		return
	}
	resp.Diagnostics.Append(setPromptState(ctx, resp.State.Set, p, plan)...)
}

func (r *promptResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state promptResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetPrompt(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read prompt", err.Error())
		return
	}
	resp.Diagnostics.Append(setPromptState(ctx, resp.State.Set, p, state)...)
}

func (r *promptResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state promptResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := expandPromptUpdate(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.UpdatePrompt(ctx, state.ID.ValueString(), in)
	if err != nil {
		resp.Diagnostics.AddError("Could not update prompt", err.Error())
		return
	}
	resp.Diagnostics.Append(setPromptState(ctx, resp.State.Set, p, plan)...)
}

func (r *promptResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state promptResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeletePrompt(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete prompt", err.Error())
	}
}

func (r *promptResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandPromptUpdate builds the PATCH body, omitting the content fields when
// they have not changed.
//
// This is the whole reason Update takes prior state. Re-sending an unchanged
// version asks the service to publish it again, and `UNIQUE (prompt_id,
// version)` makes that a duplicate-key error — on an update that may only have
// touched the description. The three content fields move together because the
// service treats them as one publish: sending a body without a version is
// refused, by design.
func expandPromptUpdate(ctx context.Context, plan, state promptResourceModel) (client.PromptUpdate, diag.Diagnostics) {
	in := client.PromptUpdate{
		// Never empty: an empty name would blank the row on this COALESCE route.
		Name:        nonEmptyString(plan.Name),
		Description: clearableString(plan.Description),
	}

	planVars, diags := expandStringList(ctx, plan.Variables)
	if diags.HasError() {
		return in, diags
	}
	stateVars, d := expandStringList(ctx, state.Variables)
	diags.Append(d...)
	if diags.HasError() {
		return in, diags
	}

	contentChanged := !plan.Body.Equal(state.Body) ||
		!plan.Version.Equal(state.Version) ||
		!equalStrings(planVars, stateVars)
	if !contentChanged {
		return in, diags
	}

	v := plan.Version.ValueString()
	b := plan.Body.ValueString()
	in.Version, in.Body, in.Variables = &v, &b, &planVars
	return in, diags
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// setPromptState writes the API's prompt into state.
//
// `body` and `variables` come from the PLAN rather than the response: the list
// route does not return a body at all, and the PATCH response carries the row
// rather than the version's content. Taking them from the plan is what keeps an
// apply consistent — and it is safe because a divergence would have to come from
// the service rewriting content it was just given, which it does not do. Read
// refreshes them from GET, which does return the body.
func setPromptState(
	ctx context.Context,
	set func(context.Context, any) diag.Diagnostics,
	p *client.Prompt,
	prior promptResourceModel,
) diag.Diagnostics {
	var diags diag.Diagnostics
	m := promptResourceModel{
		ID:          types.StringValue(p.ID),
		Name:        types.StringValue(p.Name),
		Description: optionalString(&p.Description),
		Version:     types.StringValue(p.Version),
		Body:        prior.Body,
		Variables:   prior.Variables,
		UsedBy:      types.Int64Value(int64(p.UsedBy)),
		CreatedAt:   types.StringValue(p.CreatedAt),
		ModifiedAt:  types.StringValue(p.ModifiedAt),
	}
	if p.Body != "" {
		m.Body = types.StringValue(p.Body)
	}
	// Same rule for variables, and it is what makes IMPORT work: on an import
	// there is no prior state to fall back on, so the response is the only
	// source. A response that carries them wins; one that does not (the PATCH
	// reply, which returns the row rather than the version) leaves the plan's.
	if p.Variables != nil {
		vars, d := flattenStringList(ctx, p.Variables)
		diags.Append(d...)
		m.Variables = vars
	}
	return set(ctx, m)
}
