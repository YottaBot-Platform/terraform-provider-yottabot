package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func TestPolicy_TypeName(t *testing.T) {
	var resp fwresource.MetadataResponse
	NewPolicyResource().Metadata(context.Background(),
		fwresource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_policy" {
		t.Errorf("TypeName = %q", resp.TypeName)
	}
}

// THE property of this resource. The server assigns each statement's evaluation
// position from the order it was sent, and a deny short-circuits an allow — so
// two configs with the same statements in a different order are DIFFERENT
// policies.
//
// A SetNestedAttribute is order-insensitive, so with one the framework could
// hand the provider a reordered slice and Terraform would report no diff while
// the policy silently changed meaning. That is why this must be a list, and why
// asserting on the type is worth a test.
func TestPolicySchema_StatementsAreOrderedNotASet(t *testing.T) {
	var resp fwresource.SchemaResponse
	NewPolicyResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)

	a, ok := resp.Schema.Attributes["statements"]
	if !ok {
		t.Fatal("statements attribute is missing")
	}
	if _, isSet := a.(schema.SetNestedAttribute); isSet {
		t.Fatal("statements is a SET — order is evaluation order here, and a set would let " +
			"Terraform reorder the policy into meaning something else with no diff")
	}
	if _, isList := a.(schema.ListNestedAttribute); !isList {
		t.Fatalf("statements is %T, want schema.ListNestedAttribute", a)
	}
}

// Order in must be order out: expandStatements preserves the config order,
// because that order becomes `position` server-side.
func TestExpandStatements_PreservesOrder(t *testing.T) {
	list := statementsList(t,
		stmt{sid: "first", effect: "deny", actions: []string{"agents:write"}},
		stmt{sid: "second", effect: "allow", actions: []string{"agents:read"}},
		stmt{sid: "third", effect: "allow", actions: []string{"workflows:read"}},
	)
	out, diags := expandStatements(context.Background(), list)
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}
	got := []string{out[0].Sid, out[1].Sid, out[2].Sid}
	want := []string{"first", "second", "third"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v — position is assigned from this order", got, want)
		}
	}
}

// An empty statements list must reach the wire as `[]`, not as an omitted key:
// the server leaves statements alone when the field is absent, so emptying the
// list in config would silently do nothing and the plan would never converge.
func TestExpandPolicyUpdate_EmptyListClearsRatherThanPreserving(t *testing.T) {
	empty, d := types.ListValueFrom(context.Background(),
		types.ObjectType{AttrTypes: statementObjectTypes()}, []policyStatementModel{})
	if d.HasError() {
		t.Fatalf("build empty list: %v", d)
	}
	in, diags := expandPolicyUpdate(context.Background(), policyResourceModel{
		Name:       types.StringValue("p"),
		Statements: empty,
	})
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}
	if in.Statements == nil {
		t.Fatal("statements is a nil pointer — the server would leave them unchanged")
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(body["statements"]) != "[]" {
		t.Errorf("statements = %s, want [] so the set is cleared", body["statements"])
	}
}

// Unknown is the one case where omitting — and so preserving — is right.
func TestExpandPolicyUpdate_UnknownStatementsPreserves(t *testing.T) {
	in, diags := expandPolicyUpdate(context.Background(), policyResourceModel{
		Name:       types.StringValue("p"),
		Statements: types.ListUnknown(types.ObjectType{AttrTypes: statementObjectTypes()}),
	})
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}
	if in.Statements != nil {
		t.Error("unknown statements must send nothing so the stored set is preserved")
	}
}

// Same COALESCE-without-NULLIF dialect as roles and llm_gateways.
func TestExpandPolicyUpdate_NameNeverEmptyDescriptionClearable(t *testing.T) {
	in, _ := expandPolicyUpdate(context.Background(), policyResourceModel{
		Name:        types.StringValue(""),
		Description: types.StringNull(),
		Statements:  types.ListNull(types.ObjectType{AttrTypes: statementObjectTypes()}),
	})
	if in.Name != nil {
		t.Errorf("name = %q — an empty name would blank the row on this service", *in.Name)
	}
	if in.Description == nil || *in.Description != "" {
		t.Errorf("description = %v, want a pointer to \"\"", in.Description)
	}
}

// `kind` must not be settable. The API accepts kind:"system" from any caller
// with groups:write, and a system policy refuses BOTH update and delete — so
// exposing it would let a practitioner create a row Terraform can never change
// or destroy, recoverable only by direct SQL.
func TestPolicy_KindIsComputedAndNeverSent(t *testing.T) {
	var resp fwresource.SchemaResponse
	NewPolicyResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)
	kind, ok := resp.Schema.Attributes["kind"]
	if !ok {
		t.Fatal("kind is missing")
	}
	if !kind.IsComputed() || kind.IsOptional() || kind.IsRequired() {
		t.Error("kind must be Computed only — a settable kind can produce an undestroyable policy")
	}

	// And it must not exist on either write body, where it would be settable
	// regardless of the schema.
	for name, v := range map[string]any{
		"create": client.PolicyCreate{},
		"update": client.PolicyUpdate{},
	} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		var body map[string]any
		if err := json.Unmarshal(b, &body); err != nil {
			t.Fatalf("unmarshal %s: %v", name, err)
		}
		if _, present := body["kind"]; present {
			t.Errorf("%s body carries `kind`", name)
		}
	}
}

// Attachments are not managed, for the reason yottabot_role documents. Adding
// them would make Terraform delete attachments made in the console.
func TestPolicySchema_DoesNotManageAttachments(t *testing.T) {
	var resp fwresource.SchemaResponse
	NewPolicyResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)
	for _, name := range []string{"role_ids", "roles", "attached_roles"} {
		if _, ok := resp.Schema.Attributes[name]; ok {
			t.Errorf("schema declares %q — attachments belong in their own resource", name)
		}
	}
	att, ok := resp.Schema.Attributes["attached"]
	if !ok {
		t.Fatal("attached is missing — it is how a config observes attachments without owning them")
	}
	if !att.IsComputed() || att.IsOptional() {
		t.Error("attached must be Computed only")
	}
}

// A policy with no statements flattens to null, matching a config that omitted
// the attribute — otherwise an omitted block would diff against an empty list
// forever.
func TestFlattenPolicy_NoStatementsIsNull(t *testing.T) {
	m, diags := flattenPolicy(context.Background(), &client.Policy{
		ID: "p1", Name: "p", Kind: "custom",
	}, types.ListNull(types.ObjectType{AttrTypes: statementObjectTypes()}))
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if !m.Statements.IsNull() {
		t.Errorf("statements = %v, want null for a policy with none", m.Statements)
	}
}

func TestFlattenPolicy_RoundTripsStatements(t *testing.T) {
	m, diags := flattenPolicy(context.Background(), &client.Policy{
		ID: "p1", Name: "p", Kind: "custom", Attached: "sre, oncall",
		Statements: []client.PolicyStatement{
			{Sid: "a", Effect: "deny", Actions: []string{"agents:write"}, Position: 0},
			{Sid: "", Effect: "allow", Actions: []string{"agents:read"},
				Resources: []string{"*"}, Position: 1},
		},
	}, types.ListNull(types.ObjectType{AttrTypes: statementObjectTypes()}))
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if n := len(m.Statements.Elements()); n != 2 {
		t.Fatalf("%d statements, want 2", n)
	}
	if m.Attached.ValueString() != "sre, oncall" {
		t.Errorf("attached = %q", m.Attached.ValueString())
	}

	var models []policyStatementModel
	if d := m.Statements.ElementsAs(context.Background(), &models, false); d.HasError() {
		t.Fatalf("read back: %v", d)
	}
	// An absent sid collapses to null, so a config that omitted it round-trips.
	if !models[1].Sid.IsNull() {
		t.Errorf("empty sid = %v, want null", models[1].Sid)
	}
	// An empty resources list likewise.
	if !models[0].Resources.IsNull() {
		t.Errorf("empty resources = %v, want null", models[0].Resources)
	}
}

func TestStatementEffects_MatchTheServerCheck(t *testing.T) {
	if len(statementEffects) != 2 {
		t.Errorf("effects = %v, want exactly allow and deny — the server's CHECK has changed",
			statementEffects)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────

type stmt struct {
	sid     string
	effect  string
	actions []string
}

func statementsList(t *testing.T, in ...stmt) types.List {
	t.Helper()
	ctx := context.Background()
	models := make([]policyStatementModel, 0, len(in))
	for _, s := range in {
		actions, d := types.ListValueFrom(ctx, types.StringType, s.actions)
		if d.HasError() {
			t.Fatalf("build actions: %v", d)
		}
		models = append(models, policyStatementModel{
			Sid:              types.StringValue(s.sid),
			Effect:           types.StringValue(s.effect),
			Actions:          actions,
			Resources:        types.ListNull(types.StringType),
			ResourceSelector: jsontypes.NewNormalizedNull(),
		})
	}
	l, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: statementObjectTypes()}, models)
	if d.HasError() {
		t.Fatalf("build list: %v", d)
	}
	return l
}

// `actions` and `resources` are `text[] NOT NULL` server-side, and a nil slice
// marshals to `null`. A server that passed that through answered an opaque 500
// for a statement that simply omitted `resources` — which is a legitimate
// policy, "these actions, anywhere". Fixed server-side too, but the provider
// states the empty set rather than depending on which version it is talking to.
//
// Asserting on the marshalled bytes, since nil and empty differ only there.
func TestExpandStatements_OmittedArraysMarshalAsEmptyNotNull(t *testing.T) {
	list := statementsList(t, stmt{sid: "s", effect: "allow", actions: []string{"agents:read"}})
	out, diags := expandStatements(context.Background(), list)
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}
	b, err := json.Marshal(out[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(body["resources"]) != "[]" {
		t.Errorf("resources = %s, want [] — null hit a NOT NULL column and returned 500",
			body["resources"])
	}
}

// A policy with no statements is ambiguous on the wire — the server answers
// identically whether the config omitted `statements` or set it to `[]` — but
// Terraform treats those as different values and rejects an apply whose result
// does not match its config. The prior value breaks the tie.
//
// The acceptance suite found this at step 5, after four green steps: config said
// `statements = []`, the provider returned null, and Terraform reported
// "Provider produced inconsistent result after apply".
func TestFlattenPolicy_EmptyStatementsFollowThePriorValue(t *testing.T) {
	ctx := context.Background()
	objType := types.ObjectType{AttrTypes: statementObjectTypes()}
	emptyPrior, d := types.ListValueFrom(ctx, objType, []policyStatementModel{})
	if d.HasError() {
		t.Fatalf("build empty: %v", d)
	}
	none := &client.Policy{ID: "p1", Name: "p", Kind: "custom"}

	// Config said `statements = []` — state must be an empty list, not null.
	m, diags := flattenPolicy(ctx, none, emptyPrior)
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if m.Statements.IsNull() {
		t.Error("prior was an empty list but state came back null — Terraform rejects that " +
			"as an inconsistent result after apply")
	}

	// Config omitted the attribute — state must stay null, or an omitted block
	// would diff against an empty list forever.
	m, diags = flattenPolicy(ctx, none, types.ListNull(objType))
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if !m.Statements.IsNull() {
		t.Errorf("prior was null but state came back %v", m.Statements)
	}
}

// Drift must still be reported. A policy whose statements were deleted outside
// Terraform had a NON-null prior, so it comes back as an empty list rather than
// reading as "attribute absent" and vanishing from the plan.
func TestFlattenPolicy_ExternallyEmptiedStatementsShowAsDrift(t *testing.T) {
	ctx := context.Background()
	objType := types.ObjectType{AttrTypes: statementObjectTypes()}
	priorWithTwo := statementsList(t,
		stmt{sid: "a", effect: "deny", actions: []string{"agents:write"}},
		stmt{sid: "b", effect: "allow", actions: []string{"agents:read"}},
	)
	m, diags := flattenPolicy(ctx, &client.Policy{ID: "p1", Name: "p", Kind: "custom"}, priorWithTwo)
	if diags.HasError() {
		t.Fatalf("flatten: %v", diags)
	}
	if m.Statements.IsNull() {
		t.Fatal("statements deleted server-side came back null; the plan would show no drift")
	}
	if n := len(m.Statements.Elements()); n != 0 {
		t.Errorf("%d statements, want 0 — the server has none", n)
	}
	_ = objType
}
