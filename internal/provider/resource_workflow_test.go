package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

const minimalDefinition = `{"steps":[{"name":"audit","type":"agent_call","agent":"RepoAuditor"}]}`

func TestWorkflowResource_TypeName(t *testing.T) {
	var resp resource.MetadataResponse
	NewWorkflowResource().Metadata(context.Background(),
		resource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_workflow" {
		t.Errorf("TypeName = %q, want yottabot_workflow", resp.TypeName)
	}
}

func workflowSchema(t *testing.T) resource.SchemaResponse {
	t.Helper()
	var resp resource.SchemaResponse
	NewWorkflowResource().(*workflowResource).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp
}

// definition_json must carry the JSON custom type, or equivalent JSON written
// with different key order diffs on every plan. This is the whole reason the
// attribute is not a plain string.
func TestWorkflowResource_DefinitionUsesSemanticJSONEquality(t *testing.T) {
	s := workflowSchema(t)
	attr, ok := s.Schema.Attributes["definition_json"]
	if !ok {
		t.Fatal("schema is missing definition_json")
	}
	if attr.GetType() != (jsontypes.NormalizedType{}) {
		t.Errorf("definition_json type = %T, want jsontypes.NormalizedType — a plain string diffs on key order",
			attr.GetType())
	}
	if !attr.IsRequired() {
		t.Error("definition_json should be Required")
	}
}

// Two byte-different but semantically identical definitions must compare equal,
// which is the property that stops the perpetual diff.
func TestWorkflowDefinition_KeyOrderDoesNotDiff(t *testing.T) {
	a := jsontypes.NewNormalizedValue(`{"steps":[{"name":"a","type":"agent_call"}]}`)
	b := jsontypes.NewNormalizedValue("{\n  \"steps\": [ { \"type\": \"agent_call\", \"name\": \"a\" } ]\n}")

	equal, diags := a.StringSemanticEquals(context.Background(), b)
	if diags.HasError() {
		t.Fatalf("StringSemanticEquals: %v", diags)
	}
	if !equal {
		t.Error("reordered/whitespaced JSON compared unequal — every plan would show a diff")
	}
}

func TestWorkflowResource_ComputedFieldsAreNotInputs(t *testing.T) {
	s := workflowSchema(t)
	for _, name := range []string{"id", "orchestrator_id", "last_run_at", "last_run_status", "created_at", "modified_at"} {
		attr, ok := s.Schema.Attributes[name]
		if !ok {
			t.Fatalf("schema is missing %q", name)
		}
		if !attr.IsComputed() {
			t.Errorf("%q must be Computed", name)
		}
	}
}

// The API accepts `schedule` and stores `cron`. Accepting it here would write
// config that no apply could ever reconcile, so it is refused — and the message
// has to say what to write instead, or it reads as a provider bug.
func TestWorkflowTriggerValidator_RefusesTheAliasWithAnActionableMessage(t *testing.T) {
	for _, alias := range []string{"schedule", "scheduled", "SCHEDULE", " schedule "} {
		t.Run(alias, func(t *testing.T) {
			var resp validator.StringResponse
			workflowTriggerValidator{}.ValidateString(context.Background(), validator.StringRequest{
				ConfigValue: types.StringValue(alias),
			}, &resp)

			if !resp.Diagnostics.HasError() {
				t.Fatalf("%q was accepted — it would be stored as \"cron\" and diff forever", alias)
			}
			msg := resp.Diagnostics.Errors()[0].Detail()
			if !strings.Contains(msg, "cron") {
				t.Errorf("message does not name the replacement: %s", msg)
			}
		})
	}
}

func TestWorkflowTriggerValidator_AcceptsTheStoredVocabulary(t *testing.T) {
	for _, ok := range []string{"manual", "cron", "webhook", "event", "CRON", " manual "} {
		var resp validator.StringResponse
		workflowTriggerValidator{}.ValidateString(context.Background(), validator.StringRequest{
			ConfigValue: types.StringValue(ok),
		}, &resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("%q rejected: %v", ok, resp.Diagnostics)
		}
	}
}

func TestWorkflowTriggerValidator_RefusesNonsense(t *testing.T) {
	var resp validator.StringResponse
	workflowTriggerValidator{}.ValidateString(context.Background(), validator.StringRequest{
		ConfigValue: types.StringValue("nightly"),
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Error("an arbitrary trigger was accepted")
	}
}

// Guard against the provider's vocabulary drifting from StoredTriggers.
func TestWorkflowTriggers_MatchTheStoredVocabulary(t *testing.T) {
	want := map[string]bool{"manual": true, "cron": true, "webhook": true, "event": true}
	if len(workflowTriggers) != len(want) {
		t.Fatalf("trigger vocabulary = %v, want %v", workflowTriggers, want)
	}
	for _, got := range workflowTriggers {
		if !want[got] {
			t.Errorf("unexpected trigger %q", got)
		}
	}
}

func TestExpandWorkflow_SendsTheDefinition(t *testing.T) {
	in, diags := expandWorkflow(workflowResourceModel{
		Name:           types.StringValue("nightly-repo-audit"),
		Trigger:        types.StringValue("cron"),
		CronSchedule:   types.StringValue("0 7 * * 1"),
		DefinitionJSON: jsontypes.NewNormalizedValue(minimalDefinition),
	})
	if diags.HasError() {
		t.Fatalf("expandWorkflow: %v", diags)
	}
	if len(in.Definition) == 0 {
		t.Fatal("definition not sent")
	}
	var parsed map[string]any
	if err := json.Unmarshal(in.Definition, &parsed); err != nil {
		t.Fatalf("definition is not valid JSON on the wire: %v", err)
	}
	if in.Trigger != "cron" {
		t.Errorf("trigger = %q", in.Trigger)
	}
	if in.CronSchedule == nil || *in.CronSchedule != "0 7 * * 1" {
		t.Errorf("cron_schedule = %v", in.CronSchedule)
	}
}

// Switching a workflow off a cron trigger must clear the stale expression,
// not leave it on the row. "" is the clear signal for this column.
func TestExpandWorkflow_ClearsCronScheduleWhenRemoved(t *testing.T) {
	in, _ := expandWorkflow(workflowResourceModel{
		Name:           types.StringValue("w"),
		Trigger:        types.StringValue("manual"),
		CronSchedule:   types.StringNull(),
		DefinitionJSON: jsontypes.NewNormalizedValue(minimalDefinition),
	})
	if in.CronSchedule == nil {
		t.Fatal("cron_schedule omitted — the server would preserve the stale expression")
	}
	if *in.CronSchedule != "" {
		t.Errorf("cron_schedule = %q, want the empty clear signal", *in.CronSchedule)
	}
}

func TestExpandWorkflow_NormalizesTriggerCase(t *testing.T) {
	in, _ := expandWorkflow(workflowResourceModel{
		Name:           types.StringValue("w"),
		Trigger:        types.StringValue("  CRON "),
		CronSchedule:   types.StringValue("* * * * *"),
		DefinitionJSON: jsontypes.NewNormalizedValue(minimalDefinition),
	})
	if in.Trigger != "cron" {
		t.Errorf("trigger = %q, want the canonical lower-case form", in.Trigger)
	}
}

func TestFlattenWorkflow_RoundTripIsStable(t *testing.T) {
	row := &client.Workflow{
		ID:           "66666666-6666-6666-6666-666666666666",
		Name:         "nightly-repo-audit",
		Status:       "available",
		Trigger:      "cron",
		CronSchedule: ptr("0 7 * * 1"),
		Definition:   json.RawMessage(minimalDefinition),
		Description:  ptr("Audits the repo nightly."),
		CreatedAt:    "2026-08-27T00:00:00Z",
		ModifiedAt:   "2026-08-27T00:00:00Z",
	}

	state, diags := flattenWorkflow(row)
	if diags.HasError() {
		t.Fatalf("flattenWorkflow: %v", diags)
	}
	in, diags := expandWorkflow(state)
	if diags.HasError() {
		t.Fatalf("expandWorkflow: %v", diags)
	}

	if in.Name != row.Name || in.Status != row.Status || in.Trigger != row.Trigger {
		t.Errorf("scalar round trip lost: %+v", in)
	}
	equal, _ := jsontypes.NewNormalizedValue(string(in.Definition)).
		StringSemanticEquals(context.Background(), jsontypes.NewNormalizedValue(minimalDefinition))
	if !equal {
		t.Errorf("definition round trip changed the JSON: %s", in.Definition)
	}
}

// A workflow created without a definition is a documented state (waiting on
// YAML import). definition_json is Required, so it cannot be null in state —
// the empty object is the only representation the schema permits.
func TestFlattenWorkflow_EmptyDefinitionBecomesEmptyObject(t *testing.T) {
	state, diags := flattenWorkflow(&client.Workflow{
		ID: "x", Name: "n", Status: "draft", Trigger: "manual",
	})
	if diags.HasError() {
		t.Fatalf("flattenWorkflow: %v", diags)
	}
	if state.DefinitionJSON.IsNull() {
		t.Fatal("definition_json is null, but the attribute is Required")
	}
	if got := state.DefinitionJSON.ValueString(); got != "{}" {
		t.Errorf("definition_json = %q, want {}", got)
	}
}

// The server canonicalizes the trigger; state records what it actually
// returned, so state never disagrees with the platform.
func TestFlattenWorkflow_RecordsTheServersCanonicalTrigger(t *testing.T) {
	state, _ := flattenWorkflow(&client.Workflow{
		ID: "x", Name: "n", Status: "draft", Trigger: "cron",
		Definition: json.RawMessage(minimalDefinition),
	})
	if state.Trigger.ValueString() != "cron" {
		t.Errorf("trigger = %q", state.Trigger.ValueString())
	}
}

func TestExpandWorkflow_RejectsInvalidJSON(t *testing.T) {
	_, diags := expandWorkflow(workflowResourceModel{
		Name:           types.StringValue("w"),
		DefinitionJSON: jsontypes.NewNormalizedValue(`{"steps":[`),
	})
	if !diags.HasError() {
		t.Fatal("invalid JSON reached the wire — the jsonb cast error names neither the field nor the problem")
	}
}

// A cron trigger with no expression is accepted by the API and then simply
// never fires — the kind of silent success that is worse than an error.
func TestValidateCronSchedule(t *testing.T) {
	cases := []struct {
		name      string
		trigger   types.String
		schedule  types.String
		wantError bool
	}{
		{"cron with a schedule", types.StringValue("cron"), types.StringValue("0 7 * * 1"), false},
		{"cron with no schedule", types.StringValue("cron"), types.StringNull(), true},
		{"cron with a blank schedule", types.StringValue("cron"), types.StringValue("   "), true},
		{"manual needs none", types.StringValue("manual"), types.StringNull(), false},
		{"webhook needs none", types.StringValue("webhook"), types.StringNull(), false},
		{"case and space tolerant", types.StringValue(" CRON "), types.StringNull(), true},
		// Unknown values come from another resource's output; judging them
		// now would fail plans that are actually fine.
		{"unknown schedule is not judged", types.StringValue("cron"), types.StringUnknown(), false},
		{"unknown trigger is not judged", types.StringUnknown(), types.StringNull(), false},
		// trigger unset defaults to manual server-side.
		{"absent trigger is not judged", types.StringNull(), types.StringNull(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := validateCronSchedule(tc.trigger, tc.schedule)
			if got := diags.HasError(); got != tc.wantError {
				t.Errorf("HasError = %v, want %v (%v)", got, tc.wantError, diags)
			}
		})
	}
}
