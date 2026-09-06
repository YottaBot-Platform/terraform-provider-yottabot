package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

// ── yottabot_skill ──────────────────────────────────────────────────────────
//
// A skill THIS ACCOUNT owns. The same table also holds the Yotta-managed
// catalog, and that is the whole story of this resource.
//
// ONE TABLE, TWO OWNERSHIPS, NO NAMING CUE. A catalog row simply has no account
// on it. Reads span both — seeing the catalog is the point of having one — while
// every write reaches only the caller's own rows, because the account predicate
// lives in the SQL behind them and a catalog row can never match it. Point this
// resource at a managed skill and it answers 404: not a permissions error, a
// deliberate refusal to confirm anything about a row you cannot write.
//
// THREE THINGS A CUSTOMER MAY NOT CLAIM, each enforced server-side and none of
// them exposed here:
//
//   - the account — taken from the caller, never the body, or this would be a
//     way to mint a catalog row.
//   - `source_kind` — pinned to `customer`. The catalog's own value is `yotta`,
//     and `imported` opens a provenance gate (pinned commit, license, an
//     approved review before it may go active) which is a governance workflow,
//     not something a declarative config asserts about itself.
//   - `visibility: yotta_managed` — the catalog marker. A customer row carrying
//     it would read as Yotta-authored everywhere it is surfaced.
//
// SLUG AND DOMAIN FORCE REPLACEMENT. The update route accepts neither, and that
// is deliberate rather than an oversight: a skill's slug is how everything
// refers to it, and its domain decides which investigations consider it at all.
// Changing either is a different skill.
//
// The resource is addressed internally by id, not slug, because a slug is NOT
// unique across the two ownership sets — a customer skill and a managed one may
// legitimately share one.

var (
	_ resource.Resource                = (*skillResource)(nil)
	_ resource.ResourceWithConfigure   = (*skillResource)(nil)
	_ resource.ResourceWithImportState = (*skillResource)(nil)
)

func NewSkillResource() resource.Resource { return &skillResource{} }

type skillResource struct {
	client *client.Client
}

type skillResourceModel struct {
	ID     types.String `tfsdk:"id"`
	Slug   types.String `tfsdk:"slug"`
	Title  types.String `tfsdk:"title"`
	Domain types.String `tfsdk:"domain"`

	Visibility types.String `tfsdk:"visibility"`
	Status     types.String `tfsdk:"status"`

	SourceKind types.String `tfsdk:"source_kind"`

	CreatedAt  types.String `tfsdk:"created_at"`
	ModifiedAt types.String `tfsdk:"modified_at"`
}

var (
	skillDomains  = []string{"k8s", "aws", "context", "alert", "generic"}
	skillStatuses = []string{"draft", "active", "retired"}
	// Narrower than the column's CHECK: `yotta_managed` is the catalog marker
	// and is not offered.
	skillVisibilities = []string{"customer_visible", "private"}
)

func (r *skillResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill"
}

func (r *skillResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *skillResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An SRE skill owned by your account.\n\n" +
			"The Yotta-managed skill library lives in the same place and is **read-only**: this " +
			"resource can only create, change and destroy your own skills. Pointing it at a managed " +
			"skill returns not-found.\n\n" +
			"`source_kind` is always `customer` for a skill created here, and `visibility` cannot be " +
			"set to `yotta_managed` — that value marks the managed library.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"slug": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Stable identifier for the skill, unique among your own. May " +
					"coincide with a managed skill's slug without colliding. Changing it forces " +
					"replacement — the slug is how everything refers to the skill.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"title": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Human-readable name. Editable in place.",
			},
			"domain": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "One of `k8s`, `aws`, `context`, `alert`, `generic`. Decides which " +
					"investigations consider this skill, so changing it forces replacement.",
				Validators:    []validator.String{oneOfValidator(skillDomains)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"visibility": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "`private` (default) or `customer_visible`. `yotta_managed` is " +
					"rejected: it marks the Yotta-managed library. Defaults to `private` so a skill " +
					"authored by config does not widen its audience by omission.",
				Validators: []validator.String{oneOfValidator(skillVisibilities)},
			},
			"status": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "`draft` (default), `active` or `retired`.",
				Validators:          []validator.String{oneOfValidator(skillStatuses)},
			},
			"source_kind": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Always `customer` for a skill this provider creates. Computed: " +
					"`yotta` marks the managed library, and `imported` opens a provenance review " +
					"workflow that a config cannot assert about itself.",
			},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *skillResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan skillResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.client.CreateSkill(ctx, client.SkillWrite{
		Slug:       plan.Slug.ValueString(),
		Title:      plan.Title.ValueString(),
		Domain:     plan.Domain.ValueString(),
		Visibility: nonEmptyString(plan.Visibility),
		Status:     nonEmptyString(plan.Status),
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not create skill", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenSkill(s))...)
}

// Read resolves by ID, via the list, rather than by the slug the read route
// takes.
//
// Two reasons, and the first is decisive: on IMPORT only the id is known, so a
// slug-based read requests an empty slug and fails. The second is that a slug is
// not unique across the two ownership sets — a customer skill and a
// Yotta-managed one may legitimately share one — so a slug lookup can return a
// row this resource did not create, and would then have to be checked anyway.
// Matching on the id has neither problem and is one code path instead of two.
//
// The list spans both ownership sets, which is fine here: we are looking for a
// specific id we already own. It is a library rather than an event stream, so
// the cost is a single small request.
func (r *skillResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state skillResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	skills, err := r.client.ListSkills(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Could not read skill", err.Error())
		return
	}
	id := state.ID.ValueString()
	for i := range skills {
		if skills[i].ID == id {
			resp.Diagnostics.Append(resp.State.Set(ctx, flattenSkill(&skills[i]))...)
			return
		}
	}
	// Gone, or never ours.
	resp.State.RemoveResource(ctx)
}

func (r *skillResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state skillResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Slug and domain are RequiresReplace, so they never reach an update.
	s, err := r.client.UpdateSkill(ctx, state.ID.ValueString(), client.SkillWrite{
		Title:      plan.Title.ValueString(),
		Visibility: nonEmptyString(plan.Visibility),
		Status:     nonEmptyString(plan.Status),
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not update skill", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenSkill(s))...)
}

func (r *skillResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state skillResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteSkill(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete skill", err.Error())
	}
}

func (r *skillResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func flattenSkill(s *client.Skill) skillResourceModel {
	return skillResourceModel{
		ID:         types.StringValue(s.ID),
		Slug:       types.StringValue(s.Slug),
		Title:      types.StringValue(s.Title),
		Domain:     types.StringValue(s.Domain),
		Visibility: types.StringValue(s.Visibility),
		Status:     types.StringValue(s.Status),
		SourceKind: types.StringValue(s.SourceKind),
		CreatedAt:  types.StringValue(s.CreatedAt),
		ModifiedAt: types.StringValue(s.ModifiedAt),
	}
}
