package provider

import (
	"context"
	"encoding/json"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/YottaBot-Platform/terraform-provider-yottabot/internal/client"
)

func TestServiceAccount_TypeName(t *testing.T) {
	var resp fwresource.MetadataResponse
	NewServiceAccountResource().Metadata(context.Background(),
		fwresource.MetadataRequest{ProviderTypeName: "yottabot"}, &resp)
	if resp.TypeName != "yottabot_service_account" {
		t.Errorf("TypeName = %q", resp.TypeName)
	}
}

// THE test for this resource. The API accepts `mint_credential: true` and
// answers with a ONE-SHOT plaintext private key. Terraform writes every
// attribute it receives into state, so a credential attribute here would put a
// long-lived secret in the state file in plaintext, for anyone with read access
// to the backend, forever.
//
// Checked on the schema AND on the create body, because either one alone could
// reintroduce it: a schema attribute makes it settable, and a body field makes
// it requestable even without a schema attribute.
func TestServiceAccount_NeverHandsOutCredentials(t *testing.T) {
	var resp fwresource.SchemaResponse
	NewServiceAccountResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)
	for _, forbidden := range []string{
		"mint_credential", "credential", "private_key", "private_key_pem", "secret", "client_secret",
	} {
		if _, ok := resp.Schema.Attributes[forbidden]; ok {
			t.Errorf("schema declares %q — Terraform writes state in plaintext, so a credential "+
				"attribute leaves a long-lived secret in the state file", forbidden)
		}
	}

	b, err := json.Marshal(client.ServiceAccountCreate{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := body["mint_credential"]; present {
		t.Error("the create body carries mint_credential — the API would answer with a " +
			"plaintext key the provider would then have to put somewhere")
	}
}

// `status` is not settable: retirement is the destroy path and also revokes
// every active credential, so a writable status could un-retire an account whose
// credentials are gone — a live-looking identity that cannot authenticate.
func TestServiceAccount_StatusIsComputedOnly(t *testing.T) {
	var resp fwresource.SchemaResponse
	NewServiceAccountResource().Schema(context.Background(), fwresource.SchemaRequest{}, &resp)
	st, ok := resp.Schema.Attributes["status"]
	if !ok {
		t.Fatal("status is missing — it is how a config observes a retired account")
	}
	if !st.IsComputed() || st.IsOptional() || st.IsRequired() {
		t.Error("status must be Computed only")
	}

	b, _ := json.Marshal(client.ServiceAccountUpdate{})
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	for _, forbidden := range []string{"status", "kind"} {
		if _, present := body[forbidden]; present {
			t.Errorf("the update body carries %q", forbidden)
		}
	}
}

// Both writable fields are Required, so neither may be sent empty: an empty
// username fails the service's own validation, and an empty owner group would
// orphan ownership.
func TestServiceAccountUpdate_NeitherFieldSentEmpty(t *testing.T) {
	in := client.ServiceAccountUpdate{
		Username:     nonEmptyString(types.StringValue("")),
		OwnerGroupID: nonEmptyString(types.StringNull()),
	}
	if in.Username != nil {
		t.Errorf("username = %q, want nil", *in.Username)
	}
	if in.OwnerGroupID != nil {
		t.Errorf("owner_group_id = %q, want nil", *in.OwnerGroupID)
	}
}

func TestFlattenServiceAccount_SurfacesRetirement(t *testing.T) {
	m := flattenServiceAccount(&client.ServiceAccount{
		ID: "sa-1", Username: "ci-runner", Status: "retired",
		OwnerGroupID: "g-1", OwnerGroupName: "Engineers",
	})
	if m.Status.ValueString() != "retired" {
		t.Errorf("status = %q — a destroyed account is retired, not gone, and the "+
			"config should be able to see that", m.Status.ValueString())
	}
	if m.OwnerGroupName.ValueString() != "Engineers" {
		t.Errorf("owner_group_name = %q", m.OwnerGroupName.ValueString())
	}
}
