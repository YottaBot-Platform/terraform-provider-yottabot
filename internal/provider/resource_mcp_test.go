package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func TestMCPResources_TypeNames(t *testing.T) {
	for _, tc := range []struct {
		res  resource.Resource
		want string
	}{
		{NewMCPGatewayResource(), "yottabot_mcp_gateway"},
		{NewMCPToolResource(), "yottabot_mcp_tool"},
	} {
		var resp resource.MetadataResponse
		tc.res.Metadata(context.Background(),
			resource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
		if resp.TypeName != tc.want {
			t.Errorf("TypeName = %q, want %q", resp.TypeName, tc.want)
		}
	}
}

func mcpGatewaySchema(t *testing.T) resource.SchemaResponse {
	t.Helper()
	var resp resource.SchemaResponse
	NewMCPGatewayResource().(*mcpGatewayResource).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp
}

func mcpToolSchema(t *testing.T) resource.SchemaResponse {
	t.Helper()
	var resp resource.SchemaResponse
	NewMCPToolResource().(*mcpToolResource).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp
}

// health_status is operational state of deployed instances; status is registry
// lifecycle. The server split them back apart deliberately after having folded
// them together, so conflating them here would undo that.
func TestMCPGateway_HealthIsComputedAndSeparateFromStatus(t *testing.T) {
	s := mcpGatewaySchema(t)

	health, ok := s.Schema.Attributes["health_status"]
	if !ok {
		t.Fatal("schema is missing health_status")
	}
	if !health.IsComputed() || health.IsOptional() || health.IsRequired() {
		t.Error("health_status must be Computed-only — it describes live instances, not desired state")
	}

	status, ok := s.Schema.Attributes["status"]
	if !ok {
		t.Fatal("schema is missing status")
	}
	if !status.IsOptional() {
		t.Error("status is the lifecycle field and must be settable")
	}
}

// The gateway status vocabulary carries `deprecated`, which agents, workflows
// and tools do not. Copying one of the others' lists here would silently refuse
// a value the API accepts.
func TestMCPGatewayStatuses_IncludeDeprecated(t *testing.T) {
	want := map[string]bool{"draft": true, "available": true, "unavailable": true, "deprecated": true}
	if len(mcpGatewayStatuses) != len(want) {
		t.Fatalf("gateway statuses = %v, want %v", mcpGatewayStatuses, want)
	}
	for _, got := range mcpGatewayStatuses {
		if !want[got] {
			t.Errorf("unexpected gateway status %q", got)
		}
	}
	// The tool vocabulary must NOT have grown the extra value.
	for _, got := range mcpToolStatuses {
		if got == "deprecated" {
			t.Error("tool statuses picked up `deprecated`, which the tools CHECK does not accept")
		}
	}
}

func TestMCPTransports(t *testing.T) {
	want := []string{"streamable-http", "stdio", "sse"}
	if len(mcpTransports) != len(want) {
		t.Fatalf("transports = %v, want %v", mcpTransports, want)
	}
	for i := range want {
		if mcpTransports[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, mcpTransports[i], want[i])
		}
	}
}

// The gateway PATCH route is the odd one out: nil pointers preserve, so an
// emptied description must send a pointer to "" rather than omit the key.
func TestExpandMCPGatewayUpdate_ClearsDescription(t *testing.T) {
	in := expandMCPGatewayUpdate(mcpGatewayResourceModel{
		Name:        types.StringValue("github-mcp"),
		Endpoint:    types.StringValue("https://mcp.example.com/mcp"),
		Description: types.StringNull(),
	})
	if in.Description == nil {
		t.Fatal("description omitted — a nil pointer PRESERVES on this route, so it could never be cleared")
	}
	if *in.Description != "" {
		t.Errorf("description = %q, want the empty clear signal", *in.Description)
	}
}

// name and endpoint are rejected as empty by the service, so they must never be
// sent empty — unlike description they are not clearable.
func TestExpandMCPGatewayUpdate_NeverSendsEmptyNameOrEndpoint(t *testing.T) {
	in := expandMCPGatewayUpdate(mcpGatewayResourceModel{
		Name:     types.StringNull(),
		Endpoint: types.StringNull(),
	})
	if in.Name != nil {
		t.Errorf("name = %q sent; the service rejects an explicit empty name", *in.Name)
	}
	if in.Endpoint != nil {
		t.Errorf("endpoint = %q sent; the service rejects an explicit empty endpoint", *in.Endpoint)
	}
}

// The wire field is "tools", not "tools_count". Reading the column name from
// the migration instead of the JSON tag would silently yield zero forever.
func TestFlattenMCPGateway_ReadsToolsCountFromTheWireField(t *testing.T) {
	var gw client.MCPGateway
	if err := json.Unmarshal([]byte(`{"id":"g1","name":"n","endpoint":"e","tools":7}`), &gw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if gw.ToolsCount != 7 {
		t.Fatalf("ToolsCount = %d, want 7 — the JSON tag must be \"tools\"", gw.ToolsCount)
	}
	m := flattenMCPGateway(&gw)
	if m.ToolsCount.ValueInt64() != 7 {
		t.Errorf("tools_count = %d, want 7", m.ToolsCount.ValueInt64())
	}
}

func TestMCPGatewayRoundTrip_IsStable(t *testing.T) {
	row := &client.MCPGateway{
		ID: "88888888-8888-8888-8888-888888888888", Name: "github-mcp",
		Endpoint: "https://mcp.example.com/mcp", Transport: "streamable-http",
		Status: "available", Description: "GitHub MCP gateway.",
		HealthStatus: "healthy", ToolsCount: 12,
		CreatedAt: "2026-08-27T00:00:00Z", ModifiedAt: "2026-08-27T00:00:00Z",
	}
	state := flattenMCPGateway(row)
	in := expandMCPGatewayUpdate(state)

	if in.Name == nil || *in.Name != row.Name {
		t.Errorf("name round trip: %v", in.Name)
	}
	if in.Transport == nil || *in.Transport != row.Transport {
		t.Errorf("transport round trip: %v", in.Transport)
	}
	if in.Status == nil || *in.Status != row.Status {
		t.Errorf("status round trip: %v", in.Status)
	}
	if in.Description == nil || *in.Description != row.Description {
		t.Errorf("description round trip: %v", in.Description)
	}
}

// `provider` is a Terraform meta-argument and cannot be a resource attribute —
// the framework rejects it outright (fwschema.ReservedResourceAttributeNames).
// The API field is `provider`; this is the rename Terraform forces.
func TestMCPTool_VendorReplacesTheReservedProviderName(t *testing.T) {
	s := mcpToolSchema(t)

	if _, exists := s.Schema.Attributes["provider"]; exists {
		t.Error("schema declares `provider`, a reserved Terraform meta-argument — the framework rejects it")
	}
	if _, ok := s.Schema.Attributes["vendor"]; !ok {
		t.Error("schema is missing `vendor`, the stand-in for the API's `provider` field")
	}
}

// The API field really is `provider`, so the rename must not reach the wire.
func TestExpandMCPTool_VendorMapsToTheProviderField(t *testing.T) {
	in, diags := expandMCPTool(mcpToolResourceModel{
		Name:   types.StringValue("github.mcp"),
		Vendor: types.StringValue("GitHub"),
	})
	if diags.HasError() {
		t.Fatalf("expandMCPTool: %v", diags)
	}
	if in.Provider == nil || *in.Provider != "GitHub" {
		t.Fatalf("vendor did not reach the API's provider field: %v", in.Provider)
	}
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"provider":"GitHub"`) {
		t.Errorf("wire body does not carry provider: %s", body)
	}
	if strings.Contains(string(body), `"vendor"`) {
		t.Errorf("the Terraform-only name leaked onto the wire: %s", body)
	}
}

// type is Computed and always sent as `mcp`. Letting it be configured would let
// this resource silently manage a non-MCP tool.
func TestExpandMCPTool_AlwaysSendsTypeMCP(t *testing.T) {
	in, _ := expandMCPTool(mcpToolResourceModel{Name: types.StringValue("x")})
	if in.Type != "mcp" {
		t.Errorf("type = %q, want mcp", in.Type)
	}

	s := mcpToolSchema(t)
	typeAttr, ok := s.Schema.Attributes["type"]
	if !ok {
		t.Fatal("schema is missing type")
	}
	if typeAttr.IsOptional() || typeAttr.IsRequired() {
		t.Error("type must be Computed-only — a configurable type would let this resource manage other tool types")
	}
}

func TestExpandMCPTool_ClearsOptionalStrings(t *testing.T) {
	in, _ := expandMCPTool(mcpToolResourceModel{
		Name:        types.StringValue("github.mcp"),
		Description: types.StringNull(),
		Version:     types.StringNull(),
		Vendor:      types.StringNull(),
		SecretRef:   types.StringNull(),
		Tags:        types.StringNull(),
	})
	for name, got := range map[string]*string{
		"description": in.Description,
		"version":     in.Version,
		"provider":    in.Provider,
		"secret_ref":  in.SecretRef,
		"tags":        in.Tags,
	} {
		if got == nil {
			t.Errorf("%s is nil — omission PRESERVES on this route, so it could never be cleared", name)
			continue
		}
		if *got != "" {
			t.Errorf("%s = %q, want the empty clear signal", name, *got)
		}
	}
}

func TestExpandMCPTool_RejectsInvalidJSON(t *testing.T) {
	_, diags := expandMCPTool(mcpToolResourceModel{
		Name:       types.StringValue("x"),
		ConfigJSON: jsontypes.NewNormalizedValue(`{"a":`),
	})
	if !diags.HasError() {
		t.Fatal("invalid config_json reached the wire")
	}
}

func TestMCPToolRoundTrip_IsStable(t *testing.T) {
	row := &client.MCPTool{
		ID: "99999999-9999-9999-9999-999999999999", Name: "github.mcp",
		Type: "mcp", Status: "available",
		Description: ptr("GitHub MCP tools."), Version: ptr("1.0.0"),
		Provider: ptr("GitHub"), SecretRef: ptr("secret-ref-1"),
		Config:    json.RawMessage(`{"base_url":"https://mcp.example.com/mcp"}`),
		CreatedAt: "2026-08-27T00:00:00Z", ModifiedAt: "2026-08-27T00:00:00Z",
	}
	state := flattenMCPTool(row)
	in, diags := expandMCPTool(state)
	if diags.HasError() {
		t.Fatalf("expandMCPTool: %v", diags)
	}

	if in.Name != row.Name || in.Status != row.Status || in.Type != "mcp" {
		t.Errorf("scalar round trip lost: %+v", in)
	}
	if in.Provider == nil || *in.Provider != "GitHub" {
		t.Errorf("vendor/provider round trip lost: %v", in.Provider)
	}
	if in.SecretRef == nil || *in.SecretRef != "secret-ref-1" {
		t.Errorf("secret_ref round trip lost: %v", in.SecretRef)
	}
	equal, _ := jsontypes.NewNormalizedValue(string(in.Config)).
		StringSemanticEquals(context.Background(),
			jsontypes.NewNormalizedValue(`{"base_url":"https://mcp.example.com/mcp"}`))
	if !equal {
		t.Errorf("config round trip changed the JSON: %s", in.Config)
	}
}

func TestFlattenMCPTool_EmptyConfigBecomesEmptyObject(t *testing.T) {
	m := flattenMCPTool(&client.MCPTool{ID: "x", Name: "n", Type: "mcp", Status: "draft"})
	if m.ConfigJSON.IsNull() {
		t.Fatal("config_json is null, but the attribute is Computed")
	}
	if got := m.ConfigJSON.ValueString(); got != "{}" {
		t.Errorf("config_json = %q, want {}", got)
	}
}

// secret_ref is a reference, not a value — but it must not be marked Sensitive
// either, or plan output becomes unreadable for a field that carries no secret.
func TestMCPTool_SecretRefIsAReferenceNotASecret(t *testing.T) {
	s := mcpToolSchema(t)
	attr, ok := s.Schema.Attributes["secret_ref"]
	if !ok {
		t.Fatal("schema is missing secret_ref")
	}
	if attr.IsSensitive() {
		t.Error("secret_ref holds a reference, not a secret value; marking it Sensitive hides a harmless string")
	}
	if !strings.Contains(attr.GetMarkdownDescription(), "reference") {
		t.Error("secret_ref docs must say it is a reference, so nobody pastes a real secret into it")
	}
}
