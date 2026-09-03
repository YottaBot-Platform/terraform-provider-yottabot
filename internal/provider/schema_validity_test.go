package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// The framework validates schemas when Terraform asks for them, not when they
// are declared — so a schema that is Required AND Computed, or a list with no
// element type, compiles and unit-tests fine and then fails at `terraform
// plan`. This drives the REAL provider server and asks for the schema exactly
// as Terraform would, which is the earliest point that check can happen
// without the terraform binary installed.
//
// It also means every resource added in steps 6-8 is schema-checked for free
// the moment it is registered.
func TestProviderSchema_IsValidToTheFramework(t *testing.T) {
	ctx := context.Background()

	srv, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatalf("build provider server: %v", err)
	}

	resp, err := srv.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema: %v", err)
	}
	for _, d := range resp.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Errorf("schema error: %s — %s", d.Summary, d.Detail)
		}
	}

	if resp.Provider == nil {
		t.Fatal("no provider schema returned")
	}

	// Every resource the provider claims to offer must actually have a schema.
	want := []string{
		"yottabot_agent",
		"yottabot_workflow",
		"yottabot_context_provider",
		"yottabot_mcp_gateway",
		"yottabot_mcp_tool",
		"yottabot_guardrail_policy",
		"yottabot_llm_gateway",
		"yottabot_role",
		"yottabot_group",
	}
	for _, name := range want {
		if _, ok := resp.ResourceSchemas[name]; !ok {
			t.Errorf("resource %q has no schema (registered in Resources()?)", name)
		}
	}
	if len(resp.ResourceSchemas) != len(want) {
		t.Errorf("resource set = %d schemas, want %d — update this list when a resource lands",
			len(resp.ResourceSchemas), len(want))
	}
}

// Terraform reserves a handful of names as resource meta-arguments; the
// framework refuses a schema that declares one, so this would fail at
// `terraform plan` rather than at compile time.
//
// It is not a hypothetical: the API's own field for yottabot_mcp_tool's vendor
// string is `provider`, which is reserved. That attribute ships as `vendor` for
// this reason, and this guard is what stops the next resource re-introducing
// the problem.
func TestProviderSchema_NoReservedAttributeNames(t *testing.T) {
	// fwschema.ReservedResourceAttributeNames, which is internal to the
	// framework and so restated here.
	reserved := map[string]bool{
		"connection": true, "count": true, "depends_on": true, "for_each": true,
		"lifecycle": true, "provider": true, "provisioner": true,
	}

	srv, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatalf("build provider server: %v", err)
	}
	resp, err := srv.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema: %v", err)
	}

	for name, rs := range resp.ResourceSchemas {
		if rs.Block == nil {
			continue
		}
		for _, attr := range rs.Block.Attributes {
			if reserved[attr.Name] {
				t.Errorf("%s declares reserved attribute %q — Terraform would require special syntax, "+
					"and the framework rejects the schema", name, attr.Name)
			}
		}
	}
}
