package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func TestLLMGateway_TypeName(t *testing.T) {
	var resp resource.MetadataResponse
	NewLLMGatewayResource().Metadata(context.Background(),
		resource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_llm_gateway" {
		t.Errorf("TypeName = %q", resp.TypeName)
	}
}

// The wire field is `provider`, which Terraform reserves as a meta-argument.
// yottabot_mcp_tool renamed its one to `vendor`; that name is taken here by a
// different concept, so this resource uses `upstream_provider`. Both must be
// present, and they must be distinct.
func TestLLMGateway_UpstreamProviderAndVendorAreSeparateAttributes(t *testing.T) {
	var resp fwresource.SchemaResponse
	NewLLMGatewayResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)

	if _, ok := resp.Schema.Attributes["provider"]; ok {
		t.Fatal("`provider` is a Terraform meta-argument and must never be an attribute")
	}
	up, ok := resp.Schema.Attributes["upstream_provider"]
	if !ok {
		t.Fatal("upstream_provider is missing")
	}
	if !up.IsRequired() {
		t.Error("upstream_provider must be Required — the create route demands it")
	}
	v, ok := resp.Schema.Attributes["vendor"]
	if !ok {
		t.Fatal("vendor is missing")
	}
	if !v.IsComputed() || v.IsRequired() || v.IsOptional() {
		t.Error("vendor must be Computed only — it is the gateway's owner, not an input")
	}
}

// The service says it outright: "Provider is intentionally not updatable
// (changing it is a new gateway)". CreateGatewayInput carries it and
// UpdateGatewayInput does not, so without RequiresReplace Terraform would plan
// an in-place update that silently does nothing.
func TestLLMGateway_UpstreamProviderForcesReplacement(t *testing.T) {
	var resp fwresource.SchemaResponse
	NewLLMGatewayResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)

	if got := requiresReplaceCount(t, resp.Schema.Attributes["upstream_provider"]); got != 1 {
		t.Errorf("upstream_provider has %d RequiresReplace plan modifiers, want 1 — "+
			"without it Terraform plans an in-place update the API silently ignores", got)
	}
	if got := requiresReplaceCount(t, resp.Schema.Attributes["endpoint"]); got != 0 {
		t.Errorf("endpoint must NOT force replacement; the update route accepts it (got %d)", got)
	}
}

// requiresReplaceCount counts RequiresReplace plan modifiers on a string
// attribute.
//
// Matched on the modifier's TYPE, not its description. The description reads
// "If the value of this attribute changes, Terraform will destroy and recreate
// the resource" — it never contains the word "replace", so the obvious
// substring check silently counts zero and the test passes on a resource that
// has no RequiresReplace at all. That is the failure mode this whole test
// exists to catch, so it must not be the failure mode of the test itself.
func requiresReplaceCount(t *testing.T, a schema.Attribute) int {
	t.Helper()
	sa, ok := a.(schema.StringAttribute)
	if !ok {
		t.Fatalf("attribute is %T, want schema.StringAttribute", a)
	}
	n := 0
	for _, pm := range sa.PlanModifiers {
		if strings.Contains(fmt.Sprintf("%T", pm), "requiresReplace") {
			n++
		}
	}
	return n
}

// Nil preserves on this COALESCE route, so a removed optional attribute has to
// send an explicit empty string or the removal is silently ignored.
func TestExpandLLMGatewayUpdate_ClearsRemovedOptionalStrings(t *testing.T) {
	in := expandLLMGatewayUpdate(llmGatewayResourceModel{
		Name:          types.StringValue("gw"),
		Description:   types.StringNull(),
		Endpoint:      types.StringNull(),
		CredentialRef: types.StringNull(),
	})
	for name, got := range map[string]*string{
		"description":    in.Description,
		"endpoint":       in.Endpoint,
		"credential_ref": in.CredentialRef,
	} {
		if got == nil || *got != "" {
			t.Errorf("%s = %v, want a pointer to \"\" so the service clears it", name, got)
		}
	}
}

// Two fields must never be sent empty, for different reasons.
//
// name: unlike guardrail_policies, this service does NOT guard it with NULLIF —
// its own comment says empty-string text fields are legal updates — so an empty
// name would blank the row rather than be ignored.
//
// the vocabulary fields: auth_mode, status and budget_policy are validated
// against a closed set, so "" comes back as `unknown auth_mode ""` rather than
// being read as a clear.
func TestExpandLLMGatewayUpdate_NeverSendsEmptyNameOrVocabularyFields(t *testing.T) {
	in := expandLLMGatewayUpdate(llmGatewayResourceModel{
		Name:         types.StringValue(""),
		AuthMode:     types.StringNull(),
		Status:       types.StringNull(),
		BudgetPolicy: types.StringNull(),
	})
	if in.Name != nil {
		t.Errorf("name = %q — an empty name would blank the row on this service", *in.Name)
	}
	if in.AuthMode != nil {
		t.Errorf("auth_mode = %q — the service rejects an empty vocabulary value", *in.AuthMode)
	}
	if in.Status != nil {
		t.Errorf("status = %q", *in.Status)
	}
	if in.BudgetPolicy != nil {
		t.Errorf("budget_policy = %q", *in.BudgetPolicy)
	}
}

// A null bool or number has no empty value to store, so both null and unknown
// mean "send nothing", never "write false" or "write zero". Writing false here
// would silently turn off streaming on a gateway nobody touched.
func TestOptionalScalars_NullAndUnknownBothPreserve(t *testing.T) {
	if optionalBool(types.BoolNull()) != nil || optionalBool(types.BoolUnknown()) != nil {
		t.Error("a null or unknown bool must send nothing, not false")
	}
	if optionalInt(types.Int64Null()) != nil || optionalInt64(types.Int64Unknown()) != nil {
		t.Error("a null or unknown number must send nothing, not zero")
	}
	b := optionalBool(types.BoolValue(false))
	if b == nil || *b != false {
		t.Error("an explicit false must be sent")
	}
}

func TestFlattenLLMGateway_MapsProviderOntoUpstreamProvider(t *testing.T) {
	vendor := "YottaBot"
	m := flattenLLMGateway(&client.LLMGateway{
		ID: "id-1", Name: "gw", Provider: "anthropic", Vendor: &vendor,
		Status: "available", AuthMode: "api_key", BudgetPolicy: "warn",
	})
	if m.UpstreamProvider.ValueString() != "anthropic" {
		t.Errorf("upstream_provider = %q, want anthropic", m.UpstreamProvider.ValueString())
	}
	if m.Vendor.ValueString() != "YottaBot" {
		t.Errorf("vendor = %q, want YottaBot — it is a different field", m.Vendor.ValueString())
	}
}

func TestLLMGatewayVocabularies_MatchTheServiceChecks(t *testing.T) {
	for _, tc := range []struct {
		got  []string
		want int
		name string
	}{
		{llmProviders, 6, "providers"},
		{llmAuthModes, 4, "auth modes"},
		{llmGatewayStatuses, 4, "statuses"},
		{llmBudgetPolicies, 4, "budget policies"},
	} {
		if len(tc.got) != tc.want {
			t.Errorf("%s: %d values, want %d — bot/146's CHECK has changed", tc.name, len(tc.got), tc.want)
		}
	}
}
