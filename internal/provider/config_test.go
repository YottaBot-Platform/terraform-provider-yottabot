package provider

import (
	"strings"
	"testing"
)

// envFrom builds a lookupEnv stand-in so these tests never touch the process
// environment — parallel Go tests share one, and a t.Setenv-based suite here
// would serialize the package for no reason.
func envFrom(kv map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := kv[k]
		return v, ok
	}
}

func TestResolveSettings_ConfigBeatsEnvironment(t *testing.T) {
	got := ResolveSettings(
		Settings{Endpoint: "https://from-config", Token: "config-token"},
		envFrom(map[string]string{
			"YOTTABOT_ENDPOINT": "https://from-env",
			"YOTTABOT_TOKEN":    "env-token",
		}),
	)
	if got.Endpoint != "https://from-config" {
		t.Errorf("endpoint = %q, want the provider block's value", got.Endpoint)
	}
	if got.Token != "config-token" {
		t.Errorf("token = %q, want the provider block's value", got.Token)
	}
}

func TestResolveSettings_FallsBackToEnvironment(t *testing.T) {
	got := ResolveSettings(Settings{}, envFrom(map[string]string{
		"YOTTABOT_ENDPOINT": "https://yottabot.example.com",
		"YOTTABOT_TOKEN":    "env-token",
	}))
	if got.Endpoint != "https://yottabot.example.com" || got.Token != "env-token" {
		t.Errorf("did not fall back to env: %+v", got)
	}
}

// The canonical name must outrank the compatibility alias. If the alias won,
// a stale YOTTA_ENDPOINT left over from the YottaBot CLI would silently redirect
// `terraform apply` at the wrong estate — which is the worst possible failure
// for this provider, since it would succeed.
func TestResolveSettings_CanonicalBeatsAlias(t *testing.T) {
	got := ResolveSettings(Settings{}, envFrom(map[string]string{
		"YOTTABOT_ENDPOINT": "https://canonical",
		"YOTTA_ENDPOINT":    "https://alias",
	}))
	if got.Endpoint != "https://canonical" {
		t.Errorf("endpoint = %q, want the YOTTABOT_ name to win", got.Endpoint)
	}
}

func TestResolveSettings_AliasStillWorksAlone(t *testing.T) {
	got := ResolveSettings(Settings{}, envFrom(map[string]string{
		"YOTTA_ENDPOINT": "https://alias",
		"YOTTA_TOKEN":    "alias-token",
	}))
	if got.Endpoint != "https://alias" || got.Token != "alias-token" {
		t.Errorf("compatibility alias ignored: %+v", got)
	}
}

// A trailing newline on a pasted endpoint is a real and very annoying failure:
// it surfaces as a TLS or 404 error a long way from its cause.
func TestResolveSettings_TrimsWhitespace(t *testing.T) {
	got := ResolveSettings(Settings{}, envFrom(map[string]string{
		"YOTTABOT_ENDPOINT": "  https://yottabot.example.com\n",
		"YOTTABOT_TOKEN":    "\ttoken \n",
	}))
	if got.Endpoint != "https://yottabot.example.com" {
		t.Errorf("endpoint = %q, want trimmed", got.Endpoint)
	}
	if got.Token != "token" {
		t.Errorf("token = %q, want trimmed", got.Token)
	}
}

// An all-whitespace env var must count as unset, not as a value that beats the
// alias and then fails validation with a confusing message.
func TestResolveSettings_BlankEnvIsUnset(t *testing.T) {
	got := ResolveSettings(Settings{}, envFrom(map[string]string{
		"YOTTABOT_ENDPOINT": "   ",
		"YOTTA_ENDPOINT":    "https://alias",
	}))
	if got.Endpoint != "https://alias" {
		t.Errorf("endpoint = %q, want the blank canonical to fall through to the alias", got.Endpoint)
	}
}

func TestResolveSettings_DerivesTokenURLFromEndpoint(t *testing.T) {
	got := ResolveSettings(Settings{
		Endpoint:      "https://yottabot.example.com/",
		UserID:        "svc",
		KID:           "k1",
		PrivateKeyPEM: "pem",
	}, envFrom(nil))

	want := "https://yottabot.example.com" + defaultTokenPath
	if got.TokenURL != want {
		t.Errorf("token_url = %q, want %q (trailing slash on endpoint must not double up)", got.TokenURL, want)
	}
}

func TestResolveSettings_ExplicitTokenURLIsKept(t *testing.T) {
	got := ResolveSettings(Settings{
		Endpoint: "https://yottabot.example.com",
		UserID:   "svc",
		TokenURL: "https://auth.example/oauth/token",
	}, envFrom(nil))
	if got.TokenURL != "https://auth.example/oauth/token" {
		t.Errorf("token_url = %q, want the explicit value", got.TokenURL)
	}
}

func TestSettings_Mode(t *testing.T) {
	cases := []struct {
		name string
		s    Settings
		want AuthMode
	}{
		{"nothing", Settings{}, AuthNone},
		{"token only", Settings{Token: "t"}, AuthToken},
		{"full service account", Settings{UserID: "u", KID: "k", PrivateKeyPEM: "p"}, AuthServiceAccount},
		{
			// Promoting a workspace from PAT to service account should not
			// require deleting the old variable in the same commit.
			name: "service account wins over a leftover token",
			s:    Settings{Token: "t", UserID: "u", KID: "k", PrivateKeyPEM: "p"},
			want: AuthServiceAccount,
		},
		{"half a service account falls back to token", Settings{Token: "t", UserID: "u"}, AuthToken},
		{"half a service account with no token is none", Settings{UserID: "u", KID: "k"}, AuthNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.Mode(); got != tc.want {
				t.Errorf("Mode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSettings_PartialServiceAccount(t *testing.T) {
	if (Settings{}).PartialServiceAccount() {
		t.Error("empty settings reported as a partial service account")
	}
	if (Settings{UserID: "u", KID: "k", PrivateKeyPEM: "p"}).PartialServiceAccount() {
		t.Error("complete service account reported as partial")
	}
	if !(Settings{UserID: "u", KID: "k"}).PartialServiceAccount() {
		t.Error("two-of-three not reported as partial — this is the case that degrades silently")
	}
}

func TestSettings_Validate(t *testing.T) {
	t.Run("no endpoint and no credentials reports both", func(t *testing.T) {
		errs := (Settings{}).Validate()
		if len(errs) < 2 {
			t.Fatalf("got %d errors, want both problems at once: %v", len(errs), errs)
		}
	})

	t.Run("endpoint without a scheme is rejected", func(t *testing.T) {
		errs := Settings{Endpoint: "yottabot.example.com", Token: "t"}.Validate()
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), "scheme") {
			t.Fatalf("want one scheme error, got %v", errs)
		}
	})

	t.Run("a valid token configuration passes", func(t *testing.T) {
		if errs := (Settings{Endpoint: "https://yottabot.example.com", Token: "t"}).Validate(); len(errs) != 0 {
			t.Fatalf("want no errors, got %v", errs)
		}
	})

	t.Run("a valid service account passes", func(t *testing.T) {
		s := ResolveSettings(Settings{
			Endpoint: "https://yottabot.example.com", UserID: "u", KID: "k", PrivateKeyPEM: "p",
		}, envFrom(nil))
		if errs := s.Validate(); len(errs) != 0 {
			t.Fatalf("want no errors, got %v", errs)
		}
	})

	// The message must name BOTH credential paths. A practitioner who set two
	// of three service-account fields lands here, and "set a token" would send
	// them the wrong way.
	t.Run("the no-credentials message names both paths", func(t *testing.T) {
		errs := Settings{Endpoint: "https://yottabot.example.com"}.Validate()
		if len(errs) != 1 {
			t.Fatalf("want one error, got %v", errs)
		}
		msg := errs[0].Error()
		for _, want := range []string{"token", "user_id", "kid", "private_key_pem"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message does not mention %q: %s", want, msg)
			}
		}
	})
}
