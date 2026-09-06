package provider

import (
	"context"
	"encoding/json"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func TestModel_TypeName(t *testing.T) {
	var resp fwresource.MetadataResponse
	NewModelResource().Metadata(context.Background(),
		fwresource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_model" {
		t.Errorf("TypeName = %q", resp.TypeName)
	}
}

// Routing is owned by a separate surface that sets the upstream and the gateway
// binding TOGETHER. Exposing either as writable here would let a config leave a
// model bound to a gateway with the wrong upstream id — a state that plans
// clean and fails only at dispatch.
//
// `provider` is also a Terraform meta-argument, which is why the read-only view
// of it is named `upstream_provider`.
func TestModelSchema_RoutingFieldsAreReadOnly(t *testing.T) {
	var resp fwresource.SchemaResponse
	NewModelResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)

	if _, ok := resp.Schema.Attributes["provider"]; ok {
		t.Fatal("`provider` is a Terraform meta-argument and must never be an attribute")
	}
	for _, name := range []string{"upstream_provider", "llm_gateway_id", "provider_model_id", "hosting"} {
		a, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Errorf("%q is missing — a config should still be able to observe routing", name)
			continue
		}
		if !a.IsComputed() || a.IsOptional() || a.IsRequired() {
			t.Errorf("%q must be Computed only — routing is set as one unit elsewhere", name)
		}
	}

	// And they must not exist on either write body, where they would be settable
	// regardless of the schema.
	for name, v := range map[string]any{"create": client.ModelCreate{}, "update": client.ModelUpdate{}} {
		b, _ := json.Marshal(v)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		for _, forbidden := range []string{"provider", "llm_gateway_id", "provider_model_id", "endpoint", "secret_ref"} {
			if _, present := body[forbidden]; present {
				t.Errorf("%s body carries %q", name, forbidden)
			}
		}
	}
}

// name/license/pricing/status are non-null columns with CHECK constraints and
// are NULLIF-guarded server-side, so "" fails the constraint rather than
// clearing. The free-text fields are the opposite: nil preserves, so a removed
// attribute must send "" to clear.
func TestExpandModelUpdate_GuardedFieldsNeverEmptyFreeTextClears(t *testing.T) {
	in, diags := expandModelUpdate(context.Background(), modelResourceModel{
		Name:        types.StringValue(""),
		License:     types.StringNull(),
		Pricing:     types.StringNull(),
		Status:      types.StringValue(""),
		Description: types.StringNull(),
		Tags:        types.StringNull(),
		Modalities:  types.ListNull(types.StringType),
	})
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}
	for name, got := range map[string]*string{
		"name": in.Name, "license": in.License, "pricing": in.Pricing, "status": in.Status,
	} {
		if got != nil {
			t.Errorf("%s = %q — an empty value fails the CHECK rather than clearing", name, *got)
		}
	}
	for name, got := range map[string]*string{"description": in.Description, "tags": in.Tags} {
		if got == nil || *got != "" {
			t.Errorf("%s = %v, want a pointer to \"\" so the removal takes effect", name, got)
		}
	}
}

// Modalities is a pointer to a slice so absent and empty stay distinct: absent
// leaves the stored list alone, [] clears it.
func TestExpandModelUpdate_ModalitiesUnknownPreservesEmptyClears(t *testing.T) {
	in, _ := expandModelUpdate(context.Background(), modelResourceModel{
		Name:       types.StringValue("m"),
		Modalities: types.ListUnknown(types.StringType),
	})
	if in.Modalities != nil {
		t.Error("unknown modalities must send nothing so the stored list is preserved")
	}

	empty, _ := types.ListValueFrom(context.Background(), types.StringType, []string{})
	in, _ = expandModelUpdate(context.Background(), modelResourceModel{
		Name:       types.StringValue("m"),
		Modalities: empty,
	})
	if in.Modalities == nil {
		t.Fatal("an empty list must be sent so the column clears")
	}
	b, _ := json.Marshal(in)
	var body map[string]json.RawMessage
	_ = json.Unmarshal(b, &body)
	if string(body["modalities"]) != "[]" {
		t.Errorf("modalities = %s, want []", body["modalities"])
	}
}

func TestModelVocabularies_MatchTheServerChecks(t *testing.T) {
	for name, tc := range map[string]struct {
		got  []string
		want int
	}{
		"licenses": {modelLicenses, 3},
		"pricings": {modelPricings, 4},
		"statuses": {modelStatuses, 3},
	} {
		if len(tc.got) != tc.want {
			t.Errorf("%s: %d values, want %d — the server's CHECK has changed", name, len(tc.got), tc.want)
		}
	}
}
