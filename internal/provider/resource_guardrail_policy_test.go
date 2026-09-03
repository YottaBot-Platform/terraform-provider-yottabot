package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func TestGuardrailPolicy_TypeName(t *testing.T) {
	var resp resource.MetadataResponse
	NewGuardrailPolicyResource().Metadata(context.Background(),
		resource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_guardrail_policy" {
		t.Errorf("TypeName = %q", resp.TypeName)
	}
}

// The whole reason this resource needs care. The service PATCHes with COALESCE,
// so an ABSENT field preserves the stored value. A practitioner who deletes
// `description` from their config produces a null attribute, and if that turned
// into an absent key the removal would be silently ignored and the plan would
// never converge. It has to go out as an explicit empty string.
func TestExpandGuardrailPolicy_RemovedAttributesClearRatherThanPreserve(t *testing.T) {
	in, diags := expandGuardrailPolicy(guardrailPolicyResourceModel{
		Name:        types.StringValue("keep-me"),
		Description: types.StringNull(),
		Tags:        types.StringNull(),
		Definition:  jsontypes.NewNormalizedNull(),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if in.Description == nil || *in.Description != "" {
		t.Errorf("description = %v, want a pointer to \"\" so the service clears it", in.Description)
	}
	if in.Tags == nil || *in.Tags != "" {
		t.Errorf("tags = %v, want a pointer to \"\"", in.Tags)
	}
	// definition's column is NOT NULL DEFAULT '{}', so there is no null to
	// return it to — removing it resets rather than clears.
	if string(in.Definition) != "{}" {
		t.Errorf("definition = %q, want {} — the column cannot hold null", string(in.Definition))
	}
	if in.Name != "keep-me" {
		t.Errorf("name = %q", in.Name)
	}
}

// The counterpart: an attribute not yet known this plan must send NOTHING, so
// the service keeps what it has. Sending "" here would clear a value the
// practitioner never touched.
func TestExpandGuardrailPolicy_UnknownPreserves(t *testing.T) {
	in, diags := expandGuardrailPolicy(guardrailPolicyResourceModel{
		Name:        types.StringValue("n"),
		Description: types.StringUnknown(),
		Tags:        types.StringUnknown(),
		Definition:  jsontypes.NewNormalizedUnknown(),
	})
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if in.Description != nil {
		t.Errorf("description = %v, want nil so the service preserves it", *in.Description)
	}
	if in.Tags != nil {
		t.Errorf("tags = %v, want nil", *in.Tags)
	}
	if in.Definition != nil {
		t.Errorf("definition = %q, want nil", string(in.Definition))
	}
}

// omitempty on a *string omits only a NIL pointer. A pointer to "" must
// survive into the JSON, or the clear never reaches the service — this asserts
// the wire bytes rather than the struct, because that distinction is the whole
// mechanism.
func TestGuardrailPolicyWrite_EmptyStringSurvivesSerialization(t *testing.T) {
	empty := ""
	b, err := json.Marshal(client.GuardrailPolicyWrite{Name: "n", Description: &empty})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if v, ok := got["description"]; !ok {
		t.Errorf("description was omitted from %s — the clear would never reach the service", b)
	} else if v != "" {
		t.Errorf("description = %v, want \"\"", v)
	}

	b2, err := json.Marshal(client.GuardrailPolicyWrite{Name: "n"})
	if err != nil {
		t.Fatal(err)
	}
	var got2 map[string]any
	if err := json.Unmarshal(b2, &got2); err != nil {
		t.Fatal(err)
	}
	if _, present := got2["description"]; present {
		t.Errorf("a nil description was serialized in %s — it must be absent so the service preserves", b2)
	}
}

func TestExpandGuardrailPolicy_RejectsInvalidJSON(t *testing.T) {
	_, diags := expandGuardrailPolicy(guardrailPolicyResourceModel{
		Name:       types.StringValue("n"),
		Definition: jsontypes.NewNormalizedValue("{not json"),
	})
	if !diags.HasError() {
		t.Fatal("expected an error for invalid definition JSON")
	}
}

// The other half of convergence. The service stores a cleared field as "",
// and if that came back into state as an empty string it would diff forever
// against the attribute the practitioner removed. It has to read as null.
func TestFlattenGuardrailPolicy_EmptyServerValuesReadBackAsAbsent(t *testing.T) {
	empty := ""
	m := flattenGuardrailPolicy(&client.GuardrailPolicy{
		ID: "id-1", Name: "n", Description: &empty, Tags: &empty,
	})
	if !m.Description.IsNull() {
		t.Errorf("description = %v, want null so a removed attribute stays removed", m.Description)
	}
	if !m.Tags.IsNull() {
		t.Errorf("tags = %v, want null", m.Tags)
	}
	// definition is Computed and may never be null in state.
	if m.Definition.IsNull() || m.Definition.ValueString() != "{}" {
		t.Errorf("definition = %v, want {}", m.Definition)
	}
}

// remove-from-config -> apply -> plan clean, at the unit level: what the
// service returns after a clear must expand back to the same request, or the
// next plan proposes the same change again.
func TestGuardrailPolicy_RemoveFromConfigConverges(t *testing.T) {
	empty := ""
	afterClear := &client.GuardrailPolicy{
		ID: "id-1", Name: "n", Description: &empty, Tags: &empty,
		Definition: json.RawMessage(`{}`),
	}
	state := flattenGuardrailPolicy(afterClear)

	// The practitioner's config still omits description and tags, so the plan
	// carries nulls — the same shape that produced the clear.
	replan := guardrailPolicyResourceModel{
		Name:        state.Name,
		Description: types.StringNull(),
		Tags:        types.StringNull(),
		Definition:  state.Definition,
	}
	in, diags := expandGuardrailPolicy(replan)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if in.Description == nil || *in.Description != "" {
		t.Error("a second apply would stop clearing description — the plan would not converge")
	}
	if string(in.Definition) != "{}" {
		t.Errorf("definition = %q on replan, want {}", string(in.Definition))
	}
	// And state after that second apply is identical to state after the first.
	again := flattenGuardrailPolicy(afterClear)
	if again.Description != state.Description || again.Tags != state.Tags ||
		again.Definition.ValueString() != state.Definition.ValueString() {
		t.Error("state is not stable across applies — this is a perpetual diff")
	}
}

func TestGuardrailPolicyRoundTrip_IsStable(t *testing.T) {
	desc, tags := "d", "t"
	src := &client.GuardrailPolicy{
		ID: "id-1", Name: "n", Description: &desc, Tags: &tags,
		Definition: json.RawMessage(`{"a":1}`),
		CreatedAt:  "t0", ModifiedAt: "t1",
	}
	m := flattenGuardrailPolicy(src)
	in, diags := expandGuardrailPolicy(m)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if in.Name != "n" || in.Description == nil || *in.Description != "d" ||
		in.Tags == nil || *in.Tags != "t" || string(in.Definition) != `{"a":1}` {
		t.Errorf("round trip changed the values: %+v", in)
	}
}
