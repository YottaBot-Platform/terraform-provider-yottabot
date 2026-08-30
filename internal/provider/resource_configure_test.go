package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

// Every resource's Configure unwraps the provider's client out of
// ProviderData. All five are the same six lines, which is exactly why they are
// worth testing together: the failure mode is silent. A resource that returned
// early without assigning would leave r.client nil, and the nil dereference
// lands in Create — reported to the practitioner as a plugin crash during
// apply, with nothing pointing at Configure.

func allResources() map[string]resource.Resource {
	return map[string]resource.Resource{
		"agent":            NewAgentResource(),
		"workflow":         NewWorkflowResource(),
		"context_provider": NewContextProviderResource(),
		"mcp_gateway":      NewMCPGatewayResource(),
		"mcp_tool":         NewMCPToolResource(),
	}
}

// The framework calls Configure with nil ProviderData before the provider is
// configured, and calls it again afterwards. Erroring on the first call would
// fail every run.
func TestResourceConfigure_ToleratesNilProviderData(t *testing.T) {
	for name, r := range allResources() {
		t.Run(name, func(t *testing.T) {
			cr, ok := r.(resource.ResourceWithConfigure)
			if !ok {
				t.Fatalf("%s does not implement ResourceWithConfigure — it will never receive a client", name)
			}
			var resp resource.ConfigureResponse
			cr.Configure(context.Background(), resource.ConfigureRequest{ProviderData: nil}, &resp)
			if resp.Diagnostics.HasError() {
				t.Errorf("errored on the framework's pre-configure call: %v", resp.Diagnostics)
			}
		})
	}
}

func TestResourceConfigure_AcceptsTheClient(t *testing.T) {
	c := client.New("https://x.test", client.NewStaticTokenSource("t"), nil)

	for name, r := range allResources() {
		t.Run(name, func(t *testing.T) {
			cr := r.(resource.ResourceWithConfigure)
			var resp resource.ConfigureResponse
			cr.Configure(context.Background(), resource.ConfigureRequest{ProviderData: c}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("rejected the provider's own client: %v", resp.Diagnostics)
			}
			if got := configuredClient(t, r); got != c {
				t.Error("client not stored — Create would dereference nil and crash the plugin")
			}
		})
	}
}

// Wrong ProviderData must be an error naming the type, not a panic later.
func TestResourceConfigure_RejectsWrongProviderData(t *testing.T) {
	for name, r := range allResources() {
		t.Run(name, func(t *testing.T) {
			cr := r.(resource.ResourceWithConfigure)
			var resp resource.ConfigureResponse
			cr.Configure(context.Background(),
				resource.ConfigureRequest{ProviderData: "not a client"}, &resp)

			if !resp.Diagnostics.HasError() {
				t.Fatal("wrong provider data accepted")
			}
			if d := resp.Diagnostics.Errors()[0]; !strings.Contains(d.Detail(), "string") {
				t.Errorf("error does not name the type it got: %q", d.Detail())
			}
		})
	}
}

// configuredClient reads back whichever field the resource stored its client
// in, so the test above asserts assignment rather than merely absence of error.
func configuredClient(t *testing.T, r resource.Resource) *client.Client {
	t.Helper()
	switch v := r.(type) {
	case *agentResource:
		return v.client
	case *workflowResource:
		return v.client
	case *contextProviderResource:
		return v.client
	case *mcpGatewayResource:
		return v.client
	case *mcpToolResource:
		return v.client
	default:
		t.Fatalf("unhandled resource type %T — add it here when adding a resource", r)
		return nil
	}
}
