package provider

import (
	"fmt"
	"strings"
)

// Settings is the fully resolved provider configuration — what the REST client
// is built from, after HCL, environment, and defaults have been collapsed in
// precedence order.
//
// Kept separate from the Terraform model type on purpose: resolution is
// ordinary Go over ordinary strings, so it is unit-testable without standing up
// a Terraform process. The provider's Configure method is then a thin adapter.
type Settings struct {
	Endpoint string

	// Manual/local path. A human PAT or session token.
	Token string

	// Product-automation path — service-account OAuth client credentials.
	// docs/auth-model.md: this is what real automation should use; a human
	// PAT is acceptable only where the audit story honestly says a human
	// ran Terraform.
	UserID        string
	KID           string
	PrivateKeyPEM string
	TokenURL      string
}

// AuthMode names which credential path a resolved Settings will use.
type AuthMode string

const (
	AuthNone           AuthMode = "none"
	AuthToken          AuthMode = "token"
	AuthServiceAccount AuthMode = "service_account"
)

// Mode reports which credential path these settings select.
//
// Service account wins when fully specified, because it is the path the plan
// wants product automation on; a token alongside it is treated as leftover
// config rather than an error, so promoting a workspace from PAT to service
// account does not require deleting the old variable in the same commit.
func (s Settings) Mode() AuthMode {
	if s.UserID != "" && s.KID != "" && s.PrivateKeyPEM != "" {
		return AuthServiceAccount
	}
	if s.Token != "" {
		return AuthToken
	}
	return AuthNone
}

// Env name pairs. The YOTTABOT_ prefix is canonical; YOTTA_ is accepted as a
// compatibility alias so a workspace already exporting the YottaBot CLI's
// variables
// keeps working. Canonical wins when both are set — a stale alias must never
// silently outrank the name the docs tell people to use.
var envAliases = []struct {
	canonical string
	alias     string
	field     func(*Settings) *string
}{
	{"YOTTABOT_ENDPOINT", "YOTTA_ENDPOINT", func(s *Settings) *string { return &s.Endpoint }},
	{"YOTTABOT_TOKEN", "YOTTA_TOKEN", func(s *Settings) *string { return &s.Token }},
	{"YOTTABOT_USER_ID", "YOTTA_USER_ID", func(s *Settings) *string { return &s.UserID }},
	{"YOTTABOT_KID", "YOTTA_KID", func(s *Settings) *string { return &s.KID }},
	{"YOTTABOT_PRIVATE_KEY_PEM", "YOTTA_PRIVATE_KEY_PEM", func(s *Settings) *string { return &s.PrivateKeyPEM }},
	{"YOTTABOT_TOKEN_URL", "YOTTA_TOKEN_URL", func(s *Settings) *string { return &s.TokenURL }},
}

// ResolveSettings collapses HCL config and environment into one Settings.
//
// Precedence is provider config, then environment — the order the plan
// specifies and the order every mainstream provider uses. `lookupEnv` is
// injected rather than calling os.LookupEnv directly so tests can drive the
// environment without mutating the process's.
//
// Whitespace is trimmed on every value: an endpoint pasted from a terminal
// with a trailing newline is a real and extremely annoying failure, and it
// surfaces as a TLS or 404 error far from its cause.
func ResolveSettings(cfg Settings, lookupEnv func(string) (string, bool)) Settings {
	out := Settings{
		Endpoint:      strings.TrimSpace(cfg.Endpoint),
		Token:         strings.TrimSpace(cfg.Token),
		UserID:        strings.TrimSpace(cfg.UserID),
		KID:           strings.TrimSpace(cfg.KID),
		PrivateKeyPEM: strings.TrimSpace(cfg.PrivateKeyPEM),
		TokenURL:      strings.TrimSpace(cfg.TokenURL),
	}
	for _, e := range envAliases {
		field := e.field(&out)
		if *field != "" {
			continue // explicit config wins
		}
		if v, ok := lookupEnv(e.canonical); ok && strings.TrimSpace(v) != "" {
			*field = strings.TrimSpace(v)
			continue
		}
		if v, ok := lookupEnv(e.alias); ok && strings.TrimSpace(v) != "" {
			*field = strings.TrimSpace(v)
		}
	}
	// A service account with no explicit token URL derives one from the
	// endpoint, because the machine-auth mount is fixed by the platform
	// (docs/auth-model.md) and making every practitioner restate it is
	// pure ceremony they can get wrong.
	if out.TokenURL == "" && out.Endpoint != "" && out.UserID != "" {
		out.TokenURL = strings.TrimRight(out.Endpoint, "/") + defaultTokenPath
	}
	return out
}

// defaultTokenPath is the machine-auth OAuth token mount. Kept as a constant so
// the one place that knows the platform's URL shape is greppable.
const defaultTokenPath = "/api/machine-auth/v1/oauth/token"

// Validate reports configuration that cannot produce a working client. It
// returns one error per problem so a practitioner fixes everything in a single
// edit rather than re-running `terraform plan` to discover the next one.
//
// Deliberately NOT validated here: whether the credential actually works. That
// is a network answer, and pretending to know it at configure time produces
// confidently wrong error messages.
func (s Settings) Validate() []error {
	var errs []error
	if s.Endpoint == "" {
		errs = append(errs, fmt.Errorf(
			"endpoint is required: set it in the provider block or export YOTTABOT_ENDPOINT (e.g. https://yottabot.example.com)"))
	} else if !strings.HasPrefix(s.Endpoint, "http://") && !strings.HasPrefix(s.Endpoint, "https://") {
		errs = append(errs, fmt.Errorf(
			"endpoint %q must include a scheme (https://…)", s.Endpoint))
	}

	switch s.Mode() {
	case AuthNone:
		// Name both paths. A practitioner who set two of the three
		// service-account fields is in this branch and would otherwise
		// be told only "set a token", which is the wrong fix.
		errs = append(errs, fmt.Errorf(
			"no credentials: set `token` (manual/local runs) or all of `user_id` + `kid` + `private_key_pem` "+
				"(service-account automation, preferred). Agent attestation tokens are not valid provider credentials"))
	case AuthServiceAccount:
		if s.TokenURL == "" {
			errs = append(errs, fmt.Errorf(
				"token_url could not be derived: set it explicitly, or set `endpoint` so it can default to <endpoint>%s",
				defaultTokenPath))
		}
	}
	return errs
}

// PartialServiceAccount reports a half-specified service account — the shape
// that otherwise silently falls back to token auth (or to no auth at all) and
// leaves the practitioner wondering why their private key is being ignored.
func (s Settings) PartialServiceAccount() bool {
	set := 0
	for _, v := range []string{s.UserID, s.KID, s.PrivateKeyPEM} {
		if v != "" {
			set++
		}
	}
	return set > 0 && set < 3
}
