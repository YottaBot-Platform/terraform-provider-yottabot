package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func strList(t *testing.T, vals ...string) types.List {
	t.Helper()
	l, diags := types.ListValueFrom(context.Background(), types.StringType, vals)
	if diags.HasError() {
		t.Fatalf("ListValueFrom: %v", diags)
	}
	return l
}

func ptr(s string) *string { return &s }

func TestAgentResource_TypeName(t *testing.T) {
	var resp resource.MetadataResponse
	NewAgentResource().Metadata(context.Background(),
		resource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_agent" {
		t.Errorf("TypeName = %q, want yottabot_agent", resp.TypeName)
	}
}

func agentSchema(t *testing.T) resource.SchemaResponse {
	t.Helper()
	var resp resource.SchemaResponse
	NewAgentResource().(*agentResource).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp
}

// The plan fixes which fields the platform owns. Getting these backwards is
// the difference between "Terraform adopts the server's value" and "Terraform
// fights the platform on every plan".
func TestAgentResource_ComputedFieldsAreNotInputs(t *testing.T) {
	s := agentSchema(t)

	for _, name := range []string{"id", "user_id", "created_at", "modified_at", "model_id", "orchestrator_id"} {
		attr, ok := s.Schema.Attributes[name]
		if !ok {
			t.Fatalf("schema is missing %q", name)
		}
		if !attr.IsComputed() {
			t.Errorf("%q must be Computed", name)
		}
		// orchestrator_id in particular: SaaS assigns it and a disagreeing
		// value is rejected server-side, so accepting it as input
		// would only produce apply-time 400s.
		if attr.IsRequired() {
			t.Errorf("%q must not be Required", name)
		}
	}

	if !s.Schema.Attributes["name"].IsRequired() {
		t.Error("name must be Required")
	}
}

// mint_credential returns a one-shot private key. Any field the provider can
// send or read is a field that can reach state, so it must not exist at all.
func TestAgentResource_NeverExposesMintCredential(t *testing.T) {
	s := agentSchema(t)
	for name := range s.Schema.Attributes {
		if strings.Contains(name, "credential") || strings.Contains(name, "private_key") {
			t.Errorf("schema exposes %q — credentials must not reach Terraform state", name)
		}
	}

	// And it must not appear on the wire either.
	body, err := json.Marshal(client.AgentInput{Name: "x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "mint_credential") {
		t.Errorf("create body carries mint_credential: %s", body)
	}
}

// THE perpetual-diff guard, and the reason expand exists at all. The API reads
// a nil list as "preserve"; Terraform means "make it so". If an emptied
// tool_ids sent nil, the server would keep the old tools and the next plan
// would show the same diff, forever.
func TestExpandAgent_EmptiedListsSendTheClearSignal(t *testing.T) {
	ctx := context.Background()
	in, diags := expandAgent(ctx, agentResourceModel{
		Name:               types.StringValue("RepoAuditor"),
		ToolIDs:            types.ListNull(types.StringType),
		DataSourceIDs:      types.ListNull(types.StringType),
		SecretIDs:          types.ListNull(types.StringType),
		GuardrailPolicyIDs: types.ListNull(types.StringType),
		Env:                types.MapNull(types.StringType),
	})
	if diags.HasError() {
		t.Fatalf("expandAgent: %v", diags)
	}

	for name, got := range map[string][]string{
		"tool_ids":             in.ToolIDs,
		"data_source_ids":      in.DataSourceIDs,
		"secret_ids":           in.SecretIDs,
		"guardrail_policy_ids": in.GuardrailPolicyIDs,
	} {
		if got == nil {
			t.Errorf("%s is nil — the server reads that as PRESERVE and the diff never clears", name)
		}
		if len(got) != 0 {
			t.Errorf("%s = %v, want empty", name, got)
		}
	}
	if in.Env == nil {
		t.Error("env is nil — same preserve trap as the lists")
	}

	// And it must actually serialize as [] / {}, not be dropped by omitempty.
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"tool_ids":[]`, `"data_source_ids":[]`, `"secret_ids":[]`,
		`"guardrail_policy_ids":[]`, `"env":{}`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body missing %s — an omitempty tag would silently reintroduce the preserve bug: %s", want, body)
		}
	}
}

func TestExpandAgent_PopulatedListsAreSentThrough(t *testing.T) {
	in, diags := expandAgent(context.Background(), agentResourceModel{
		Name:    types.StringValue("a"),
		ToolIDs: strList(t, "11111111-1111-1111-1111-111111111111"),
	})
	if diags.HasError() {
		t.Fatalf("expandAgent: %v", diags)
	}
	if len(in.ToolIDs) != 1 || in.ToolIDs[0] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("tool_ids = %v", in.ToolIDs)
	}
}

// description / model / tags / system_prompt are COALESCE($n, col) server-side,
// so "" clears and omission preserves. A removed attribute must send "".
func TestExpandAgent_ClearableStringsSendEmptyNotNil(t *testing.T) {
	in, diags := expandAgent(context.Background(), agentResourceModel{
		Name:         types.StringValue("a"),
		Description:  types.StringNull(),
		Model:        types.StringNull(),
		Tags:         types.StringNull(),
		SystemPrompt: types.StringNull(),
	})
	if diags.HasError() {
		t.Fatalf("expandAgent: %v", diags)
	}
	for name, got := range map[string]*string{
		"description":   in.Description,
		"model":         in.Model,
		"tags":          in.Tags,
		"system_prompt": in.SystemPrompt,
	} {
		if got == nil {
			t.Errorf("%s is nil — omission PRESERVES server-side, so the field could never be cleared", name)
			continue
		}
		if *got != "" {
			t.Errorf("%s = %q, want the empty clear signal", name, *got)
		}
	}
}

// prompt_id is the exception: NULLIF means "" preserves, so it cannot be
// cleared through this route. Sending "" would be a silent no-op, which is why
// removal forces replacement instead.
func TestExpandAgent_PromptIDIsNeverSentEmpty(t *testing.T) {
	in, _ := expandAgent(context.Background(), agentResourceModel{
		Name:     types.StringValue("a"),
		PromptID: types.StringNull(),
	})
	if in.PromptID != nil {
		t.Errorf("prompt_id = %q, want omitted — an empty value is a no-op server-side", *in.PromptID)
	}

	in, _ = expandAgent(context.Background(), agentResourceModel{
		Name:     types.StringValue("a"),
		PromptID: types.StringValue("22222222-2222-2222-2222-222222222222"),
	})
	if in.PromptID == nil || *in.PromptID != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("prompt_id not sent: %v", in.PromptID)
	}
}

// prompt_id cannot be cleared in place, so removal must force replacement.
// Without the plan modifier the resource would show the same diff on every
// plan and never converge.
func TestAgentResource_PromptIDRemovalForcesReplacement(t *testing.T) {
	s := agentSchema(t)

	attr, ok := s.Schema.Attributes["prompt_id"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("prompt_id is %T, want schema.StringAttribute", s.Schema.Attributes["prompt_id"])
	}
	if !attr.Optional {
		t.Error("prompt_id should be Optional")
	}
	if len(attr.PlanModifiers) == 0 {
		t.Fatal("prompt_id has no plan modifier — removal would diff forever instead of replacing")
	}
	if !strings.Contains(attr.MarkdownDescription, "forces replacement") {
		t.Error("prompt_id docs must warn that removal replaces the agent")
	}
}

func TestNormalizeNewlines(t *testing.T) {
	cases := map[string]string{
		"a\r\nb":    "a\nb",
		"a\rb":      "a\nb",
		"a\nb":      "a\nb",
		"a\r\n\r\n": "a\n\n",
		"plain":     "plain",
	}
	for in, want := range cases {
		if got := normalizeNewlines(in); got != want {
			t.Errorf("normalizeNewlines(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandAgent_NormalizesPromptLineEndings(t *testing.T) {
	in, _ := expandAgent(context.Background(), agentResourceModel{
		Name:         types.StringValue("a"),
		SystemPrompt: types.StringValue("line one\r\nline two\r\n"),
	})
	if in.SystemPrompt == nil {
		t.Fatal("system_prompt not sent")
	}
	if strings.Contains(*in.SystemPrompt, "\r") {
		t.Errorf("CR survived normalization: %q", *in.SystemPrompt)
	}
}

// The round trip is the property that actually matters: state written from a
// server row must produce a body that would not change that row. Anything else
// is a diff on every plan.
func TestAgentRoundTrip_IsStable(t *testing.T) {
	ctx := context.Background()
	row := &client.Agent{
		ID:                 "33333333-3333-3333-3333-333333333333",
		Name:               "RepoAuditor",
		Status:             "available",
		Description:        ptr("Reviews repository changes."),
		Model:              ptr("claude-opus-5"),
		SystemPrompt:       ptr("Be concise.\n"),
		Tags:               ptr("audit"),
		ToolIDs:            []string{"11111111-1111-1111-1111-111111111111"},
		DataSourceIDs:      []string{},
		SecretIDs:          nil,
		GuardrailPolicyIDs: []string{},
		Env:                map[string]string{"LOG_LEVEL": "debug"},
		OrchestratorID:     ptr("44444444-4444-4444-4444-444444444444"),
		UserID:             ptr("55555555-5555-5555-5555-555555555555"),
		CreatedAt:          "2026-08-27T00:00:00Z",
		ModifiedAt:         "2026-08-27T00:00:00Z",
	}

	state, diags := flattenAgent(ctx, row)
	if diags.HasError() {
		t.Fatalf("flattenAgent: %v", diags)
	}
	in, diags := expandAgent(ctx, state)
	if diags.HasError() {
		t.Fatalf("expandAgent: %v", diags)
	}

	if in.Name != row.Name {
		t.Errorf("name %q -> %q", row.Name, in.Name)
	}
	if in.Status != row.Status {
		t.Errorf("status %q -> %q", row.Status, in.Status)
	}
	if in.Description == nil || *in.Description != *row.Description {
		t.Errorf("description round trip lost: %v", in.Description)
	}
	if len(in.ToolIDs) != 1 || in.ToolIDs[0] != row.ToolIDs[0] {
		t.Errorf("tool_ids round trip: %v", in.ToolIDs)
	}
	if in.Env["LOG_LEVEL"] != "debug" {
		t.Errorf("env round trip: %v", in.Env)
	}
	// A server-side empty list must come back as an empty list, not nil: it
	// has to keep meaning "clear" on the next apply.
	if in.DataSourceIDs == nil {
		t.Error("an empty server list became nil on the round trip — that flips clear into preserve")
	}
}

// The other half of the stable round trip: the server returns "" for a cleared
// optional string, and config says null. If flatten did not collapse them, the
// two would differ forever.
func TestFlattenAgent_EmptyStringsBecomeNull(t *testing.T) {
	state, diags := flattenAgent(context.Background(), &client.Agent{
		ID: "x", Name: "n", Status: "draft",
		Description:  ptr(""),
		Model:        ptr(""),
		Tags:         ptr(""),
		SystemPrompt: ptr(""),
	})
	if diags.HasError() {
		t.Fatalf("flattenAgent: %v", diags)
	}
	for name, got := range map[string]types.String{
		"description":   state.Description,
		"model":         state.Model,
		"tags":          state.Tags,
		"system_prompt": state.SystemPrompt,
	} {
		if !got.IsNull() {
			t.Errorf("%s = %v, want null so an absent config attribute agrees with a cleared column", name, got)
		}
	}
}

func TestFlattenAgent_EmptyCollectionsBecomeNull(t *testing.T) {
	state, _ := flattenAgent(context.Background(), &client.Agent{
		ID: "x", Name: "n", Status: "draft",
		ToolIDs: []string{}, Env: map[string]string{},
	})
	if !state.ToolIDs.IsNull() {
		t.Error("empty tool_ids should be null in state, matching an absent config attribute")
	}
	if !state.Env.IsNull() {
		t.Error("empty env should be null in state")
	}
}

func TestFlattenAgent_NullableComputedFields(t *testing.T) {
	state, _ := flattenAgent(context.Background(), &client.Agent{ID: "x", Name: "n", Status: "draft"})
	for name, got := range map[string]types.String{
		"model_id":        state.ModelID,
		"orchestrator_id": state.OrchestratorID,
		"user_id":         state.UserID,
	} {
		if !got.IsNull() {
			t.Errorf("%s = %v, want null (computed attributes may not be left unknown after apply)", name, got)
		}
	}
}

func TestOneOfValidator_PinsTheStatusVocabulary(t *testing.T) {
	// Guard against the vocabularies drifting apart silently.
	for _, want := range []string{"draft", "available", "unavailable"} {
		found := false
		for _, got := range agentStatuses {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("status vocabulary is missing %q — it must match the server's CHECK", want)
		}
	}
	if len(agentStatuses) != 3 {
		t.Errorf("status vocabulary = %v, want exactly the three settable values", agentStatuses)
	}
}

func TestEnvKeyPattern(t *testing.T) {
	valid := []string{"LOG_LEVEL", "_X", "A1", "ABC_DEF_123"}
	invalid := []string{"lower", "1LEADING", "WITH-DASH", "WITH SPACE", ""}

	for _, k := range valid {
		if !envKeyPattern.MatchString(k) {
			t.Errorf("%q should be a valid env key", k)
		}
	}
	for _, k := range invalid {
		if envKeyPattern.MatchString(k) {
			t.Errorf("%q should be rejected at plan time rather than 400ing at apply", k)
		}
	}
}
