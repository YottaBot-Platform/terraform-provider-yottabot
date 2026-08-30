package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	tfprovider "github.com/hashicorp/terraform-plugin-framework/provider"
)

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// The provider type name is the prefix of every resource name in every
// customer's state file. Renaming it is a breaking change that cannot be
// undone by a patch release, so it gets a test rather than a convention.
func TestMetadata_TypeNameIsStable(t *testing.T) {
	var resp tfprovider.MetadataResponse
	New("1.2.3")().Metadata(context.Background(), tfprovider.MetadataRequest{}, &resp)

	if resp.TypeName != "yottabot" {
		t.Errorf("TypeName = %q, want yottabot (resources are named <type>_agent, …)", resp.TypeName)
	}
	if resp.Version != "1.2.3" {
		t.Errorf("Version = %q, want the injected build version", resp.Version)
	}
}

// The schema must declare every field ResolveSettings knows how to fill, or a
// documented environment variable would have no HCL counterpart.
func TestSchema_CoversEveryConfigurableField(t *testing.T) {
	var resp tfprovider.SchemaResponse
	New("dev")().Schema(context.Background(), tfprovider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	for _, want := range []string{"endpoint", "token", "user_id", "kid", "private_key_pem", "token_url"} {
		if _, ok := resp.Schema.Attributes[want]; !ok {
			t.Errorf("schema is missing %q", want)
		}
	}
}

// Credential material must never be readable from state or a plan file.
func TestSchema_SecretsAreMarkedSensitive(t *testing.T) {
	var resp tfprovider.SchemaResponse
	New("dev")().Schema(context.Background(), tfprovider.SchemaRequest{}, &resp)

	for _, name := range []string{"token", "private_key_pem"} {
		attr, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Fatalf("schema is missing %q", name)
		}
		if !attr.IsSensitive() {
			t.Errorf("%q is not marked Sensitive — it would appear in plan output", name)
		}
	}
}

func TestNewClient_SelectsTheCredentialPath(t *testing.T) {
	t.Run("token path", func(t *testing.T) {
		c, err := newClient(Settings{Endpoint: "https://yottabot.example.com", Token: "pat"})
		if err != nil {
			t.Fatalf("newClient: %v", err)
		}
		if c == nil {
			t.Fatal("nil client")
		}
	})

	t.Run("service-account path", func(t *testing.T) {
		s := ResolveSettings(Settings{
			Endpoint:      "https://yottabot.example.com",
			UserID:        "u",
			KID:           "k",
			PrivateKeyPEM: testPrivateKeyPEM(t),
		}, func(string) (string, bool) { return "", false })

		c, err := newClient(s)
		if err != nil {
			t.Fatalf("newClient: %v", err)
		}
		if c == nil {
			t.Fatal("nil client")
		}
	})

	// A malformed key must fail HERE, at configure time. Deferring it to the
	// first API call surfaces it as a 401, which reads as a permissions
	// problem and sends the practitioner to the wrong place entirely.
	t.Run("a bad private key fails at configure time", func(t *testing.T) {
		_, err := newClient(Settings{
			Endpoint:      "https://yottabot.example.com",
			UserID:        "u",
			KID:           "k",
			PrivateKeyPEM: "-----BEGIN PRIVATE KEY-----\nnonsense\n-----END PRIVATE KEY-----",
			TokenURL:      "https://yottabot.example.com/api/machine-auth/v1/oauth/token",
		})
		if err == nil {
			t.Fatal("want an error for a malformed key")
		}
		if !strings.Contains(err.Error(), "private_key_pem") {
			t.Errorf("error does not name the field: %v", err)
		}
	})

	t.Run("no credentials", func(t *testing.T) {
		if _, err := newClient(Settings{Endpoint: "https://yottabot.example.com"}); err == nil {
			t.Fatal("want an error when no credential path is selected")
		}
	})
}
