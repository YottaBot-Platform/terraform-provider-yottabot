package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func TestPrompt_TypeName(t *testing.T) {
	var resp fwresource.MetadataResponse
	NewPromptResource().Metadata(context.Background(),
		fwresource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_prompt" {
		t.Errorf("TypeName = %q", resp.TypeName)
	}
}

func promptModel(version, body string, vars ...string) promptResourceModel {
	m := promptResourceModel{
		Name:      types.StringValue("p"),
		Version:   types.StringValue(version),
		Body:      types.StringValue(body),
		Variables: types.ListNull(types.StringType),
	}
	if len(vars) > 0 {
		l, _ := types.ListValueFrom(context.Background(), types.StringType, vars)
		m.Variables = l
	}
	return m
}

// THE test for this resource. Terraform calls Update when ANY attribute
// changes, so an edit to `description` alone would re-send the unchanged
// version and ask the service to publish it again — and `UNIQUE (prompt_id,
// version)` makes that a duplicate-key error on an update that never touched
// the content.
//
// Nothing about the schema hints at this; it is only visible from the fact that
// versions are rows rather than columns.
func TestExpandPromptUpdate_UnchangedContentIsNotRepublished(t *testing.T) {
	state := promptModel("1.0.0", "you are helpful")
	plan := state
	plan.Description = types.StringValue("a new description")

	in, diags := expandPromptUpdate(context.Background(), plan, state)
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}
	if in.Version != nil || in.Body != nil || in.Variables != nil {
		t.Errorf("content fields were sent for a description-only edit "+
			"(version=%v body=%v variables=%v) — the service would try to publish "+
			"1.0.0 again and fail on a duplicate key", in.Version, in.Body, in.Variables)
	}
	if in.Description == nil || *in.Description != "a new description" {
		t.Errorf("description = %v, want the new value", in.Description)
	}
}

// The mirror image: a real content change must carry all three, because the
// service treats them as one publish and refuses a body without a version.
func TestExpandPromptUpdate_ChangedBodyPublishesWithVersionAndVariables(t *testing.T) {
	state := promptModel("1.0.0", "old body")
	plan := promptModel("1.1.0", "new body")

	in, diags := expandPromptUpdate(context.Background(), plan, state)
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}
	if in.Version == nil || *in.Version != "1.1.0" {
		t.Errorf("version = %v, want 1.1.0", in.Version)
	}
	if in.Body == nil || *in.Body != "new body" {
		t.Errorf("body = %v, want the new body", in.Body)
	}
	if in.Variables == nil {
		t.Error("variables must accompany a publish — the service refuses a partial one")
	}
}

// Variables are part of the version's contract, so changing them alone is also
// a publish. A caller binding against the declared inputs would otherwise see
// them change under a version they had pinned.
func TestExpandPromptUpdate_VariablesAloneAlsoPublish(t *testing.T) {
	state := promptModel("1.0.0", "body", "name")
	plan := promptModel("1.1.0", "body", "name", "tone")

	in, diags := expandPromptUpdate(context.Background(), plan, state)
	if diags.HasError() {
		t.Fatalf("expand: %v", diags)
	}
	if in.Version == nil || in.Body == nil || in.Variables == nil {
		t.Errorf("a variables change did not publish: version=%v body=%v variables=%v",
			in.Version, in.Body, in.Variables)
	}
	if len(*in.Variables) != 2 {
		t.Errorf("variables = %v, want two", *in.Variables)
	}
}

// A version bump with identical content still publishes — the practitioner
// asked for it explicitly, and refusing would make an intentional re-tag
// impossible.
func TestExpandPromptUpdate_VersionBumpAlonePublishes(t *testing.T) {
	state := promptModel("1.0.0", "body")
	plan := promptModel("2.0.0", "body")

	in, _ := expandPromptUpdate(context.Background(), plan, state)
	if in.Version == nil || *in.Version != "2.0.0" {
		t.Errorf("version = %v, want 2.0.0", in.Version)
	}
}

// Same COALESCE-without-NULLIF dialect: an empty name would blank the row.
func TestExpandPromptUpdate_NameNeverEmpty(t *testing.T) {
	state := promptModel("1.0.0", "b")
	plan := state
	plan.Name = types.StringValue("")

	in, _ := expandPromptUpdate(context.Background(), plan, state)
	if in.Name != nil {
		t.Errorf("name = %q — an empty name would blank the row", *in.Name)
	}
}

// The PATCH response carries the prompt row, not the version's content, and the
// list route returns no body at all. Taking body from the plan is what keeps an
// apply consistent; a response that DOES carry one wins, which is what makes a
// refresh able to detect drift.
func TestSetPromptState_KeepsPlanBodyWhenTheResponseHasNone(t *testing.T) {
	var got promptResourceModel
	capture := func(_ context.Context, v any) diag.Diagnostics {
		got = v.(promptResourceModel)
		return nil
	}
	prior := promptModel("1.1.0", "the body from config")
	_ = setPromptState(context.Background(), capture,
		&client.Prompt{ID: "p1", Name: "p", Version: "1.1.0"}, prior)
	if got.Body.ValueString() != "the body from config" {
		t.Errorf("body = %q, want the plan's — the response carried none",
			got.Body.ValueString())
	}

	_ = setPromptState(context.Background(), capture,
		&client.Prompt{ID: "p1", Name: "p", Version: "1.1.0", Body: "server body"}, prior)
	if got.Body.ValueString() != "server body" {
		t.Errorf("body = %q, want the server's — a returned body must win so a "+
			"refresh can see drift", got.Body.ValueString())
	}
}
