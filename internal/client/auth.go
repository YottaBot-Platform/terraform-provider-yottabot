package client

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// clientAssertionType is the only assertion type the token endpoint accepts.
const clientAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"

// refreshSkew is how long before expiry a cached access token is treated as
// spent. Platform access tokens live 15 minutes; a large `terraform apply` can
// easily straddle that boundary, and a token that expires mid-apply fails
// halfway through with resources already created.
const refreshSkew = 60 * time.Second

// TokenSource supplies the bearer token for each request.
//
// Access tokens are cached in memory for the life of one Terraform process and
// never persisted — writing one to disk or to state would turn a 15-minute
// credential into a durable one, which is the whole reason the plan keeps
// credential minting out of Terraform.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// staticToken is the manual/local path: a PAT or session token used verbatim.
type staticToken string

func (t staticToken) Token(context.Context) (string, error) {
	if t == "" {
		return "", fmt.Errorf("no token configured")
	}
	return string(t), nil
}

// NewStaticTokenSource returns a TokenSource over a fixed bearer token.
func NewStaticTokenSource(tok string) TokenSource { return staticToken(tok) }

// serviceAccountSource mints access tokens with the RFC 6749 client_credentials
// grant, authenticating with an RFC 7523 JWT client assertion.
type serviceAccountSource struct {
	userID   string
	kid      string
	tokenURL string
	priv     ed25519.PrivateKey
	http     *http.Client
	now      func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewServiceAccountTokenSource validates the credential material up front so a
// bad key is reported at provider-configure time rather than on the first API
// call, where it would look like a permissions problem.
func NewServiceAccountTokenSource(userID, kid, privateKeyPEM, tokenURL string, hc *http.Client) (TokenSource, error) {
	priv, err := parsePrivatePEM(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	if userID == "" || kid == "" {
		return nil, fmt.Errorf("service-account auth needs both user_id and kid")
	}
	if tokenURL == "" {
		return nil, fmt.Errorf("service-account auth needs a token_url")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &serviceAccountSource{
		userID: userID, kid: kid, tokenURL: tokenURL, priv: priv,
		http: hc, now: time.Now,
	}, nil
}

func (s *serviceAccountSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.token != "" && s.now().Add(refreshSkew).Before(s.expires) {
		return s.token, nil
	}

	now := s.now().UTC()
	assertion, err := signAssertion(s.priv, s.kid, s.userID, s.tokenURL, now)
	if err != nil {
		return "", err
	}

	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {clientAssertionType},
		"client_assertion":      {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request to %s: %w", s.tokenURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", oauthError(s.tokenURL, resp.StatusCode, body)
	}

	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("token response from %s is not JSON: %w", s.tokenURL, err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("token response from %s carried no access_token", s.tokenURL)
	}

	s.token = out.AccessToken
	// A response without expires_in is treated as short-lived rather than
	// eternal: caching a token we cannot date would silently produce 401s
	// forever once it lapsed.
	ttl := time.Duration(out.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = refreshSkew * 2
	}
	s.expires = s.now().Add(ttl)
	return s.token, nil
}

// oauthError turns an RFC 6749 error envelope into something a practitioner can
// act on. The envelope's `error` code is the actionable part — `invalid_client`
// means the credential, `access_denied` means policy refused an otherwise-valid
// credential, and sending someone to rotate a key over the latter wastes an
// afternoon.
func oauthError(tokenURL string, status int, body []byte) error {
	var env struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &env)

	switch env.Error {
	case "invalid_client":
		return fmt.Errorf("service-account authentication failed (%s): %s. "+
			"Check that user_id matches the credential's user, kid names an ACTIVE credential, "+
			"and private_key_pem is its private half", env.Error, env.Description)
	case "access_denied":
		return fmt.Errorf("the credential is valid but a conditional-access policy refused it (%s): %s. "+
			"This is a policy decision, not a bad key — do not rotate the credential", env.Error, env.Description)
	case "":
		return fmt.Errorf("token endpoint %s returned %d: %s",
			tokenURL, status, strings.TrimSpace(string(body)))
	default:
		return fmt.Errorf("token endpoint %s returned %d (%s): %s",
			tokenURL, status, env.Error, env.Description)
	}
}
