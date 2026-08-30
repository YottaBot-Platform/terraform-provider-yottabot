package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func TestContextProviderResource_TypeName(t *testing.T) {
	var resp resource.MetadataResponse
	NewContextProviderResource().Metadata(context.Background(),
		resource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_context_provider" {
		t.Errorf("TypeName = %q, want yottabot_context_provider", resp.TypeName)
	}
}

func contextProviderSchema(t *testing.T) resource.SchemaResponse {
	t.Helper()
	var resp resource.SchemaResponse
	NewContextProviderResource().(*contextProviderResource).
		Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	return resp
}

// type / external_id / discoverer appear only in the INSERT — the UPDATE
// statement does not touch them. Without RequiresReplace, changing one would
// produce an apply that silently did nothing and a diff that never cleared.
func TestContextProviderResource_ReplaceOnlyFields(t *testing.T) {
	s := contextProviderSchema(t)

	for _, name := range []string{"type", "external_id", "discoverer"} {
		attr, ok := s.Schema.Attributes[name].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s is %T, want schema.StringAttribute", name, s.Schema.Attributes[name])
		}
		if !attr.Required {
			t.Errorf("%q must be Required", name)
		}
		if len(attr.PlanModifiers) == 0 {
			t.Errorf("%q has no RequiresReplace modifier — the update route ignores it, "+
				"so an in-place change would silently do nothing", name)
		}
	}

	// display_name IS patchable and must not force replacement.
	dn, ok := s.Schema.Attributes["display_name"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("display_name is %T", s.Schema.Attributes["display_name"])
	}
	if len(dn.PlanModifiers) != 0 {
		t.Error("display_name is patchable; forcing replacement on it would destroy discovered resources needlessly")
	}
}

// These are all server-derived (cadence floors, per-discoverer ingestion
// defaults, the account default cadence). Declaring them Optional-only would
// make Terraform fight the platform on every plan.
func TestContextProviderResource_ServerDefaultedFieldsAreComputed(t *testing.T) {
	s := contextProviderSchema(t)
	for _, name := range []string{
		"poll_interval_seconds", "poll_interval_source", "ingestion_mode", "state", "discoverer_cfg_json",
	} {
		attr, ok := s.Schema.Attributes[name]
		if !ok {
			t.Fatalf("schema is missing %q", name)
		}
		if !attr.IsComputed() {
			t.Errorf("%q must be Computed — the server assigns it when config omits it", name)
		}
		if !attr.IsOptional() {
			t.Errorf("%q must also be Optional", name)
		}
	}
}

func TestContextProviderResource_ComputedOnlyFields(t *testing.T) {
	s := contextProviderSchema(t)
	for _, name := range []string{"id", "last_polled_at", "last_error", "created_at", "modified_at"} {
		attr, ok := s.Schema.Attributes[name]
		if !ok {
			t.Fatalf("schema is missing %q", name)
		}
		if !attr.IsComputed() || attr.IsOptional() || attr.IsRequired() {
			t.Errorf("%q must be Computed-only", name)
		}
	}
}

// The server does not reject source=default + explicit seconds; it silently
// ignores the seconds. Config would hold one number and the row another,
// forever. This is the same class of bug as the cron/schedule alias.
func TestValidatePollSource(t *testing.T) {
	cases := []struct {
		name      string
		source    types.String
		seconds   types.Int64
		wantError bool
	}{
		{"default with seconds is a trap", types.StringValue("default"), types.Int64Value(900), true},
		{"default alone is fine", types.StringValue("default"), types.Int64Null(), false},
		{"override with seconds is the point", types.StringValue("override"), types.Int64Value(900), false},
		{"override alone is fine", types.StringValue("override"), types.Int64Null(), false},
		{"absent source is not judged", types.StringNull(), types.Int64Value(900), false},
		{"unknown source is not judged", types.StringUnknown(), types.Int64Value(900), false},
		{"unknown seconds is not judged", types.StringValue("default"), types.Int64Unknown(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := validatePollSource(tc.source, tc.seconds)
			if got := diags.HasError(); got != tc.wantError {
				t.Errorf("HasError = %v, want %v (%v)", got, tc.wantError, diags)
			}
		})
	}
}

func TestExpandContextProvider_SendsTheRegistration(t *testing.T) {
	in, diags := expandContextProvider(contextProviderResourceModel{
		Type:              types.StringValue("github_org"),
		ExternalID:        types.StringValue("YottaBot-Platform"),
		DisplayName:       types.StringValue("YottaBot Platform GitHub"),
		Discoverer:        types.StringValue("github"),
		IngestionMode:     types.StringValue("hybrid"),
		State:             types.StringValue("active"),
		DiscovererCfgJSON: jsontypes.NewNormalizedValue(`{"include_repos":["example-repo"]}`),
	})
	if diags.HasError() {
		t.Fatalf("expandContextProvider: %v", diags)
	}
	if in.Type != "github_org" || in.ExternalID != "YottaBot-Platform" || in.Discoverer != "github" {
		t.Errorf("registration fields lost: %+v", in)
	}
	var cfg map[string]any
	if err := json.Unmarshal(in.DiscovererCfg, &cfg); err != nil {
		t.Fatalf("discoverer_cfg is not valid JSON on the wire: %v", err)
	}
}

// credential_ref is COALESCE($n, col): omission preserves, so removing it from
// config must send "" or the old reference lives on.
func TestExpandContextProvider_ClearsCredentialRef(t *testing.T) {
	in, _ := expandContextProvider(contextProviderResourceModel{
		Type: types.StringValue("t"), ExternalID: types.StringValue("e"),
		DisplayName: types.StringValue("d"), Discoverer: types.StringValue("github"),
		CredentialRef: types.StringNull(),
	})
	if in.CredentialRef == nil {
		t.Fatal("credential_ref omitted — the server would preserve the old reference")
	}
	if *in.CredentialRef != "" {
		t.Errorf("credential_ref = %q, want the empty clear signal", *in.CredentialRef)
	}
}

func TestExpandContextProvider_RejectsInvalidJSON(t *testing.T) {
	_, diags := expandContextProvider(contextProviderResourceModel{
		Type: types.StringValue("t"), ExternalID: types.StringValue("e"),
		DisplayName: types.StringValue("d"), Discoverer: types.StringValue("github"),
		DiscovererCfgJSON: jsontypes.NewNormalizedValue(`{"a":`),
	})
	if !diags.HasError() {
		t.Fatal("invalid discoverer_cfg_json reached the wire")
	}
}

func TestFlattenContextProvider_RoundTripIsStable(t *testing.T) {
	row := &client.ContextProvider{
		ID:                  "77777777-7777-7777-7777-777777777777",
		Type:                "github_org",
		ExternalID:          "YottaBot-Platform",
		DisplayName:         "YottaBot Platform GitHub",
		Discoverer:          "github",
		CredentialRef:       ptr("cred-ref-1"),
		DiscovererCfg:       json.RawMessage(`{"include_repos":["example-repo"]}`),
		PollIntervalSeconds: 900,
		PollIntervalSource:  "override",
		IngestionMode:       "hybrid",
		State:               "active",
		CreatedAt:           "2026-08-27T00:00:00Z",
		ModifiedAt:          "2026-08-27T00:00:00Z",
	}

	state, diags := flattenContextProvider(row)
	if diags.HasError() {
		t.Fatalf("flattenContextProvider: %v", diags)
	}
	in, diags := expandContextProvider(state)
	if diags.HasError() {
		t.Fatalf("expandContextProvider: %v", diags)
	}

	if in.Type != row.Type || in.ExternalID != row.ExternalID || in.Discoverer != row.Discoverer {
		t.Errorf("registration round trip lost: %+v", in)
	}
	if in.PollIntervalSeconds != 900 || in.PollIntervalSource != "override" {
		t.Errorf("cadence round trip lost: %d / %q", in.PollIntervalSeconds, in.PollIntervalSource)
	}
	if in.IngestionMode != "hybrid" || in.State != "active" {
		t.Errorf("mode/state round trip lost: %q / %q", in.IngestionMode, in.State)
	}
	equal, _ := jsontypes.NewNormalizedValue(string(in.DiscovererCfg)).
		StringSemanticEquals(context.Background(), jsontypes.NewNormalizedValue(`{"include_repos":["example-repo"]}`))
	if !equal {
		t.Errorf("discoverer_cfg round trip changed the JSON: %s", in.DiscovererCfg)
	}
}

// discoverer_cfg_json is Computed and therefore can never be null in state.
func TestFlattenContextProvider_EmptyCfgBecomesEmptyObject(t *testing.T) {
	state, _ := flattenContextProvider(&client.ContextProvider{
		ID: "x", Type: "t", ExternalID: "e", DisplayName: "d", Discoverer: "github",
	})
	if state.DiscovererCfgJSON.IsNull() {
		t.Fatal("discoverer_cfg_json is null, but the attribute is Computed")
	}
	if got := state.DiscovererCfgJSON.ValueString(); got != "{}" {
		t.Errorf("discoverer_cfg_json = %q, want {}", got)
	}
}

func TestContextProviderVocabularies(t *testing.T) {
	// Pinned against the live API's validators; drift means a value the server
	// accepts is refused at plan, or vice versa.
	for _, tc := range []struct {
		name string
		got  []string
		want []string
	}{
		{"states", contextProviderStates, []string{"active", "paused", "retired"}},
		{"ingestion modes", contextIngestionModes, []string{"poll", "hybrid", "stream"}},
		{"poll sources", contextPollSources, []string{"default", "override"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.got) != len(tc.want) {
				t.Fatalf("%v, want %v", tc.got, tc.want)
			}
			for i := range tc.want {
				if tc.got[i] != tc.want[i] {
					t.Errorf("[%d] = %q, want %q", i, tc.got[i], tc.want[i])
				}
			}
		})
	}
}
