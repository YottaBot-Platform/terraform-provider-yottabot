package provider

import (
	"context"
	"encoding/json"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func TestSkill_TypeName(t *testing.T) {
	var resp fwresource.MetadataResponse
	NewSkillResource().Metadata(context.Background(),
		fwresource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_skill" {
		t.Errorf("TypeName = %q", resp.TypeName)
	}
}

// THE guard for this resource, at the provider end. The Yotta-managed library
// lives in the same table with no naming cue, and three fields are what separate
// a customer skill from a managed one. None may be settable here — the server
// pins all three, and exposing any would let a config contradict it or, worse,
// succeed.
func TestSkillSchema_CannotClaimTheManagedLibrary(t *testing.T) {
	var resp fwresource.SchemaResponse
	NewSkillResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)

	// account ownership and provenance are not inputs at all.
	for _, forbidden := range []string{
		"account_id", "source_url", "source_commit", "license", "import_review_status",
	} {
		if _, ok := resp.Schema.Attributes[forbidden]; ok {
			t.Errorf("schema declares %q — it is server-set or belongs to the import flow", forbidden)
		}
	}

	// source_kind is visible but never settable.
	sk, ok := resp.Schema.Attributes["source_kind"]
	if !ok {
		t.Fatal("source_kind is missing — a config should be able to see it is `customer`")
	}
	if !sk.IsComputed() || sk.IsOptional() || sk.IsRequired() {
		t.Error("source_kind must be Computed only: `yotta` marks the managed library and " +
			"`imported` opens a provenance review a config cannot assert about itself")
	}

	// And the write body must not carry them either.
	b, _ := json.Marshal(client.SkillWrite{})
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	for _, forbidden := range []string{"account_id", "source_kind", "import_review_status"} {
		if _, present := body[forbidden]; present {
			t.Errorf("the write body carries %q", forbidden)
		}
	}
}

// `yotta_managed` is the catalog marker. A customer row carrying it would read
// as Yotta-authored everywhere it is surfaced, so the provider's vocabulary is
// deliberately NARROWER than the column's CHECK.
func TestSkillSchema_VisibilityExcludesTheCatalogMarker(t *testing.T) {
	for _, v := range skillVisibilities {
		if v == "yotta_managed" {
			t.Fatal("`yotta_managed` is offered as a visibility — it marks the managed library " +
				"and a customer skill must not be able to claim it")
		}
	}
	if len(skillVisibilities) != 2 {
		t.Errorf("visibilities = %v, want exactly customer_visible and private", skillVisibilities)
	}
}

// Slug and domain are not accepted by the update route: a slug is how everything
// refers to the skill, and a domain decides which investigations consider it.
// Without RequiresReplace, Terraform would plan an in-place update the API
// silently ignores and the config would never converge.
//
// Matched on the plan modifier's TYPE. Its description reads "Terraform will
// destroy and recreate the resource" and never contains the word "replace", so
// a substring check silently counts zero and passes on a resource with no
// RequiresReplace at all.
func TestSkillSchema_SlugAndDomainForceReplacement(t *testing.T) {
	var resp fwresource.SchemaResponse
	NewSkillResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)

	for _, name := range []string{"slug", "domain"} {
		if got := requiresReplaceCount(t, resp.Schema.Attributes[name]); got != 1 {
			t.Errorf("%s has %d RequiresReplace modifiers, want 1 — the update route does not "+
				"accept it, so an in-place plan would be silently ignored", name, got)
		}
	}
	// title IS editable, so it must not force replacement.
	if got := requiresReplaceCount(t, resp.Schema.Attributes["title"]); got != 0 {
		t.Errorf("title must not force replacement (got %d) — it is editable in place", got)
	}
}

// Read fetches by slug, and a slug can name BOTH a managed skill and one of
// ours — the table carries two partial unique indexes precisely so they may
// coexist. If the row that comes back is not the one this resource created, ours
// is gone and state must be dropped rather than adopting the managed row.
func TestSkillRead_IdMismatchMeansOursIsGone(t *testing.T) {
	// The behaviour lives in Read, which needs a client; this asserts the shape
	// the check depends on — that a Skill carries an id distinct from its slug,
	// so the comparison is possible at all.
	s := flattenSkill(&client.Skill{
		ID: "sk-1", Slug: "shared-slug", Title: "T", Domain: "k8s",
		Visibility: "private", Status: "draft", SourceKind: "customer",
	})
	if s.ID.ValueString() == s.Slug.ValueString() {
		t.Fatal("id and slug are the same value; the read-back check cannot distinguish " +
			"a managed skill from ours")
	}
	if s.SourceKind.ValueString() != "customer" {
		t.Errorf("source_kind = %q, want customer", s.SourceKind.ValueString())
	}
}

func TestSkillVocabularies_MatchTheServerChecks(t *testing.T) {
	if len(skillDomains) != 5 {
		t.Errorf("domains = %v, want 5 — the server's CHECK has changed", skillDomains)
	}
	if len(skillStatuses) != 3 {
		t.Errorf("statuses = %v, want 3", skillStatuses)
	}
}

// Guard against the schema growing a definition/body attribute by accident: the
// skill's content is versioned separately and is redacted for managed rows, so
// exposing it here would be a different feature with different rules.
func TestSkillSchema_DoesNotManageVersionContent(t *testing.T) {
	var resp fwresource.SchemaResponse
	NewSkillResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)
	for _, name := range []string{"body", "definition", "definition_ref", "versions", "input_schema"} {
		if _, ok := resp.Schema.Attributes[name]; ok {
			t.Errorf("schema declares %q — version content is a separate surface, and it is "+
				"redacted for Yotta-managed skills", name)
		}
	}
}
