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

// Vocabularies pinned from the server's CHECK constraints (validTransports /
// validStatuses). Note the gateway status vocabulary has FOUR values — it
// carries `deprecated`, which agents and workflows do not.
var (
	mcpTransports      = []string{"streamable-http", "stdio", "sse"}
	mcpGatewayStatuses = []string{"draft", "available", "unavailable", "deprecated"}
	mcpToolStatuses    = []string{"draft", "available", "unavailable"}
)

// ── yottabot_mcp_gateway ────────────────────────────────────────────────────

var (
	_ resource.Resource                = (*mcpGatewayResource)(nil)
	_ resource.ResourceWithConfigure   = (*mcpGatewayResource)(nil)
	_ resource.ResourceWithImportState = (*mcpGatewayResource)(nil)
)

func NewMCPGatewayResource() resource.Resource { return &mcpGatewayResource{} }

type mcpGatewayResource struct {
	client *client.Client
}

type mcpGatewayResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Endpoint    types.String `tfsdk:"endpoint"`
	Transport   types.String `tfsdk:"transport"`
	Status      types.String `tfsdk:"status"`
	Description types.String `tfsdk:"description"`

	HealthStatus    types.String `tfsdk:"health_status"`
	ToolsCount      types.Int64  `tfsdk:"tools_count"`
	CreatedByUserID types.String `tfsdk:"created_by_user_id"`
	CreatedAt       types.String `tfsdk:"created_at"`
	ModifiedAt      types.String `tfsdk:"modified_at"`
}

func (r *mcpGatewayResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_gateway"
}

func (r *mcpGatewayResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *mcpGatewayResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A registered MCP gateway — a routeable endpoint the platform can call. " +
			"This is catalog configuration only: deploying the MCP server itself belongs in your infra modules.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name":     schema.StringAttribute{Required: true, MarkdownDescription: "Gateway name."},
			"endpoint": schema.StringAttribute{Required: true, MarkdownDescription: "Gateway URL the platform calls."},
			"transport": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{oneOfValidator(mcpTransports)},
				MarkdownDescription: "`streamable-http` (default), `stdio`, or `sse`.",
			},
			"status": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{oneOfValidator(mcpGatewayStatuses)},
				MarkdownDescription: "Lifecycle status: `draft`, `available` (default), `unavailable`, or " +
					"`deprecated`. This is registry lifecycle, **not** operational health — see `health_status`.",
			},
			"description": schema.StringAttribute{Optional: true, MarkdownDescription: "Free-text description."},
			"health_status": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Operational health of deployed instances (`healthy`/`degraded`/`down`/" +
					"`unknown`). Computed and deliberately separate from `status`: one registered gateway can " +
					"have several live instances.",
			},
			"tools_count": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Number of tools the gateway exposes.",
			},
			"created_by_user_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Creator's user UUID."},
			"created_at":         schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at":        schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *mcpGatewayResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan mcpGatewayResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	gw, err := r.client.CreateMCPGateway(ctx, client.MCPGatewayCreate{
		Name:        plan.Name.ValueString(),
		Endpoint:    plan.Endpoint.ValueString(),
		Transport:   plan.Transport.ValueString(),
		Status:      plan.Status.ValueString(),
		Description: plan.Description.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Could not create MCP gateway", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenMCPGateway(gw))...)
}

func (r *mcpGatewayResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state mcpGatewayResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	gw, err := r.client.GetMCPGateway(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read MCP gateway", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenMCPGateway(gw))...)
}

func (r *mcpGatewayResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan mcpGatewayResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state mcpGatewayResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	gw, err := r.client.UpdateMCPGateway(ctx, state.ID.ValueString(), expandMCPGatewayUpdate(plan))
	if err != nil {
		resp.Diagnostics.AddError("Could not update MCP gateway", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenMCPGateway(gw))...)
}

func (r *mcpGatewayResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state mcpGatewayResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteMCPGateway(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete MCP gateway", err.Error())
	}
}

func (r *mcpGatewayResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// expandMCPGatewayUpdate builds the PATCH body.
//
// This route is the odd one out: nil POINTERS mean preserve, so a removed
// optional attribute must send a pointer to "" to clear it — not omit the key,
// which is the opposite of what omission means on the create body.
func expandMCPGatewayUpdate(m mcpGatewayResourceModel) client.MCPGatewayUpdate {
	out := client.MCPGatewayUpdate{}
	if v := m.Name.ValueString(); v != "" {
		out.Name = &v
	}
	if v := m.Endpoint.ValueString(); v != "" {
		out.Endpoint = &v
	}
	if v := m.Transport.ValueString(); v != "" {
		out.Transport = &v
	}
	if v := m.Status.ValueString(); v != "" {
		out.Status = &v
	}
	// description alone is clearable: the service rejects an empty name or
	// endpoint, but an empty description is a legitimate "remove it".
	out.Description = clearableString(m.Description)
	return out
}

func flattenMCPGateway(gw *client.MCPGateway) mcpGatewayResourceModel {
	return mcpGatewayResourceModel{
		ID:              types.StringValue(gw.ID),
		Name:            types.StringValue(gw.Name),
		Endpoint:        types.StringValue(gw.Endpoint),
		Transport:       types.StringValue(gw.Transport),
		Status:          types.StringValue(gw.Status),
		Description:     optionalString(&gw.Description),
		HealthStatus:    types.StringValue(gw.HealthStatus),
		ToolsCount:      types.Int64Value(int64(gw.ToolsCount)),
		CreatedByUserID: computedString(gw.CreatedByUserID),
		CreatedAt:       types.StringValue(gw.CreatedAt),
		ModifiedAt:      types.StringValue(gw.ModifiedAt),
	}
}

// ── yottabot_mcp_tool ───────────────────────────────────────────────────────

var (
	_ resource.Resource                = (*mcpToolResource)(nil)
	_ resource.ResourceWithConfigure   = (*mcpToolResource)(nil)
	_ resource.ResourceWithImportState = (*mcpToolResource)(nil)
)

func NewMCPToolResource() resource.Resource { return &mcpToolResource{} }

type mcpToolResource struct {
	client *client.Client
}

type mcpToolResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Type        types.String `tfsdk:"type"`
	Status      types.String `tfsdk:"status"`
	Version     types.String `tfsdk:"version"`
	// Vendor maps to the API's `provider` field. It CANNOT be called
	// `provider` here — that is a Terraform meta-argument and the framework
	// rejects it as a reserved root attribute name (ReservedResourceAttributeNames).
	Vendor     types.String         `tfsdk:"vendor"`
	ConfigJSON jsontypes.Normalized `tfsdk:"config_json"`
	SecretRef  types.String         `tfsdk:"secret_ref"`
	Tags       types.String         `tfsdk:"tags"`

	OwnerUserID types.String `tfsdk:"owner_user_id"`
	CreatedAt   types.String `tfsdk:"created_at"`
	ModifiedAt  types.String `tfsdk:"modified_at"`
}

func (r *mcpToolResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_tool"
}

func (r *mcpToolResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *mcpToolResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An MCP-backed tool catalog row — the handle agents attach to and workflows invoke. " +
			"`type` is always `mcp`; the tools catalog holds other types this resource does not manage.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Server-assigned UUID. This is the import id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Tool name. Convention is a gateway namespace such as `github.mcp`.",
			},
			"description": schema.StringAttribute{Optional: true, MarkdownDescription: "Free-text description."},
			"type": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Always `mcp`. Computed rather than configurable: this resource exists to " +
					"manage MCP tools, and letting the type be set would let it silently manage something else.",
			},
			"status": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{oneOfValidator(mcpToolStatuses)},
				MarkdownDescription: "Lifecycle status: `draft`, `available` (default), or `unavailable`.",
			},
			"version": schema.StringAttribute{Optional: true, MarkdownDescription: "Tool version string."},
			"vendor": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Display/vendor string — the API's `provider` field. It is named `vendor` " +
					"here because `provider` is a Terraform meta-argument and cannot be a resource attribute.",
			},
			"config_json": schema.StringAttribute{
				Optional:   true,
				Computed:   true,
				CustomType: jsontypes.NormalizedType{},
				MarkdownDescription: "Tool configuration as JSON. Defaults to `{}`. Compared semantically, so key " +
					"order and whitespace do not produce diffs.",
			},
			"secret_ref": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Reference to a stored secret. A reference only — never put an external " +
					"secret value here; it would land in Terraform state.",
			},
			"tags": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Tags, as the single string the current API stores (not a list).",
			},
			"owner_user_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Owning user's UUID."},
			"created_at":    schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
			"modified_at":   schema.StringAttribute{Computed: true, MarkdownDescription: "Last modification timestamp."},
		},
	}
}

func (r *mcpToolResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan mcpToolResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := expandMCPTool(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	tool, err := r.client.CreateMCPTool(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Could not create MCP tool", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenMCPTool(tool))...)
}

func (r *mcpToolResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state mcpToolResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tool, err := r.client.GetMCPTool(ctx, state.ID.ValueString())
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Could not read MCP tool", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenMCPTool(tool))...)
}

func (r *mcpToolResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan mcpToolResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state mcpToolResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, diags := expandMCPTool(plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	tool, err := r.client.UpdateMCPTool(ctx, state.ID.ValueString(), in)
	if err != nil {
		resp.Diagnostics.AddError("Could not update MCP tool", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, flattenMCPTool(tool))...)
}

func (r *mcpToolResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state mcpToolResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteMCPTool(ctx, state.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Could not delete MCP tool", err.Error())
	}
}

func (r *mcpToolResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func expandMCPTool(m mcpToolResourceModel) (client.MCPToolInput, diag.Diagnostics) {
	var diags diag.Diagnostics

	in := client.MCPToolInput{
		Name: m.Name.ValueString(),
		// Always sent, never read from config: this resource manages MCP
		// tools by definition.
		Type:   client.ToolTypeMCP,
		Status: m.Status.ValueString(),
	}
	// All COALESCE($n, col): "" clears, omission preserves.
	in.Description = clearableString(m.Description)
	in.Version = clearableString(m.Version)
	in.Provider = clearableString(m.Vendor)
	in.SecretRef = clearableString(m.SecretRef)
	in.Tags = clearableString(m.Tags)

	if !m.ConfigJSON.IsNull() && !m.ConfigJSON.IsUnknown() {
		raw := m.ConfigJSON.ValueString()
		if !json.Valid([]byte(raw)) {
			diags.AddAttributeError(path.Root("config_json"), "Invalid JSON",
				"config_json is not valid JSON")
			return in, diags
		}
		in.Config = json.RawMessage(raw)
	}
	return in, diags
}

func flattenMCPTool(t *client.MCPTool) mcpToolResourceModel {
	m := mcpToolResourceModel{
		ID:          types.StringValue(t.ID),
		Name:        types.StringValue(t.Name),
		Description: optionalString(t.Description),
		Type:        types.StringValue(t.Type),
		Status:      types.StringValue(t.Status),
		Version:     optionalString(t.Version),
		Vendor:      optionalString(t.Provider),
		SecretRef:   optionalString(t.SecretRef),
		Tags:        optionalString(t.Tags),
		OwnerUserID: computedString(t.OwnerUserID),
		CreatedAt:   types.StringValue(t.CreatedAt),
		ModifiedAt:  types.StringValue(t.ModifiedAt),
	}
	// config_json is Computed and can never be null in state.
	if len(t.Config) == 0 {
		m.ConfigJSON = jsontypes.NewNormalizedValue("{}")
	} else {
		m.ConfigJSON = jsontypes.NewNormalizedValue(string(t.Config))
	}
	return m
}
