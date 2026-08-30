package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Configure is the first provider code a practitioner's run reaches, and every
// branch in it produces a message they will act on. It was untested because
// building a ConfigureRequest by hand is fiddly; the helper below does it once.

// configureWith runs Configure against a config whose attributes are the given
// tftypes values. Any attribute omitted is null.
func configureWith(t *testing.T, vals map[string]tftypes.Value) *provider.ConfigureResponse {
	t.Helper()
	ctx := context.Background()

	p := New("test")()

	var sr provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &sr)
	if sr.Diagnostics.HasError() {
		t.Fatalf("provider schema: %v", sr.Diagnostics)
	}

	objType, ok := sr.Schema.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatalf("provider schema is not an object type")
	}

	full := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, aType := range objType.AttributeTypes {
		if v, given := vals[name]; given {
			full[name] = v
			continue
		}
		full[name] = tftypes.NewValue(aType, nil)
	}

	var resp provider.ConfigureResponse
	p.Configure(ctx, provider.ConfigureRequest{
		Config: tfsdk.Config{
			Schema: sr.Schema,
			Raw:    tftypes.NewValue(objType, full),
		},
	}, &resp)
	return &resp
}

func str(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }
func unknown() tftypes.Value     { return tftypes.NewValue(tftypes.String, tftypes.UnknownValue) }

// An unknown endpoint means it is interpolated from something not yet applied.
// Terraform calls Configure again after apply, so this must defer silently.
// Reporting "endpoint is required" here would fail every configuration that
// derives its endpoint from another resource — and the practitioner's config
// would be correct.
func TestConfigure_DefersOnUnknownValues(t *testing.T) {
	for name, vals := range map[string]map[string]tftypes.Value{
		"unknown endpoint": {"endpoint": unknown(), "token": str("t")},
		"unknown token":    {"endpoint": str("https://x.test"), "token": unknown()},
		"unknown key":      {"endpoint": str("https://x.test"), "private_key_pem": unknown()},
	} {
		t.Run(name, func(t *testing.T) {
			resp := configureWith(t, vals)
			if resp.Diagnostics.HasError() {
				t.Errorf("errored on an unknown value instead of deferring: %v", resp.Diagnostics)
			}
			if resp.ResourceData != nil {
				t.Error("built a client from an unknown configuration")
			}
		})
	}
}

func TestConfigure_BuildsAClientFromAToken(t *testing.T) {
	resp := configureWith(t, map[string]tftypes.Value{
		"endpoint": str("https://x.test"),
		"token":    str("a-token"),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("valid token configuration rejected: %v", resp.Diagnostics)
	}
	if resp.ResourceData == nil {
		t.Error("no client on ResourceData — every resource's Configure would find nil")
	}
	if resp.DataSourceData == nil {
		t.Error("no client on DataSourceData")
	}
}

// A missing endpoint is the most common first-run mistake, and the message has
// to name both the attribute and the environment variable.
func TestConfigure_MissingEndpointIsAnActionableError(t *testing.T) {
	t.Setenv("YOTTABOT_ENDPOINT", "")
	t.Setenv("YOTTA_ENDPOINT", "")
	t.Setenv("YOTTABOT_TOKEN", "")
	t.Setenv("YOTTA_TOKEN", "")

	resp := configureWith(t, map[string]tftypes.Value{"token": str("t")})
	if !resp.Diagnostics.HasError() {
		t.Fatal("missing endpoint accepted")
	}
	joined := strings.ToLower(diagText(resp))
	if !strings.Contains(joined, "endpoint") {
		t.Errorf("error does not name the attribute: %s", joined)
	}
	if !strings.Contains(joined, "yottabot_endpoint") {
		t.Errorf("error does not mention the environment variable: %s", joined)
	}
}

// A half-set service account degrades to token auth silently, so the
// practitioner sees permission errors rather than a configuration problem.
// It warns rather than erroring, because the fallback is legitimate.
func TestConfigure_WarnsOnAHalfSetServiceAccount(t *testing.T) {
	resp := configureWith(t, map[string]tftypes.Value{
		"endpoint": str("https://x.test"),
		"token":    str("t"),
		"user_id":  str("u"), // kid and private_key_pem deliberately absent
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("should fall back to token auth, not error: %v", resp.Diagnostics)
	}
	if resp.Diagnostics.WarningsCount() == 0 {
		t.Error("no warning — this is the case that otherwise looks like a permissions bug")
	}
}

// Credential material is validated here rather than on the first API call,
// where a malformed key surfaces as a 401 and reads as a permissions problem.
func TestConfigure_RejectsAMalformedPrivateKey(t *testing.T) {
	resp := configureWith(t, map[string]tftypes.Value{
		"endpoint":        str("https://x.test"),
		"user_id":         str("u"),
		"kid":             str("k"),
		"private_key_pem": str("-----BEGIN PRIVATE KEY-----\nnot base64\n-----END PRIVATE KEY-----"),
		"token_url":       str("https://x.test/token"),
	})
	if !resp.Diagnostics.HasError() {
		t.Fatal("malformed private key accepted — it would surface later as a 401")
	}
}

func diagText(resp *provider.ConfigureResponse) string {
	var b strings.Builder
	for _, d := range resp.Diagnostics {
		b.WriteString(d.Summary())
		b.WriteString(" ")
		b.WriteString(d.Detail())
		b.WriteString("\n")
	}
	return b.String()
}
