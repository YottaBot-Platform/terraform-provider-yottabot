package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestKey returns an ed25519 keypair with the private half PEM-encoded the
// way the platform issues it (PKCS#8, "PRIVATE KEY").
func newTestKey(t *testing.T) (ed25519.PublicKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	return pub, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// decodeSegment pulls one base64url JWS segment into a map.
func decodeSegment(t *testing.T, seg string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal segment: %v", err)
	}
	return m
}

// The assertion must satisfy the SERVER's verifier, not merely round-trip
// through our own code. Every assertion here corresponds to a specific check
// the machine-auth service performs on the client assertion; if one starts
// failing, the provider can no longer authenticate.
func TestSignAssertion_MatchesTheServersVerifier(t *testing.T) {
	pub, pemStr := newTestKey(t)
	priv, err := parsePrivatePEM(pemStr)
	if err != nil {
		t.Fatalf("parsePrivatePEM: %v", err)
	}

	now := time.Unix(1700000000, 0).UTC()
	tok, err := signAssertion(priv, "kid-1", "user-1", "https://yottabot.example.com/api/machine-auth/v1/oauth/token", now)
	if err != nil {
		t.Fatalf("signAssertion: %v", err)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("want a 3-part compact JWS, got %d parts", len(parts))
	}

	// The signature must verify with ed25519 over "header.payload" — the exact
	// construction crypto.VerifyJWS performs.
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		t.Fatal("signature does not verify — the server would reject this assertion")
	}

	header := decodeSegment(t, parts[0])
	if header["alg"] != "EdDSA" {
		t.Errorf("alg = %v, want EdDSA", header["alg"])
	}
	if header["typ"] != "JWT" {
		t.Errorf("typ = %v, want JWT", header["typ"])
	}
	if header["kid"] != "kid-1" {
		t.Errorf("kid = %v, want kid-1 — the server resolves the public key by this", header["kid"])
	}

	claims := decodeSegment(t, parts[1])
	if claims["iss"] != "user-1" || claims["sub"] != "user-1" {
		t.Errorf("iss/sub = %v/%v, want both to equal the user_id (RFC 7523 §3)", claims["iss"], claims["sub"])
	}
	if claims["aud"] != "https://yottabot.example.com/api/machine-auth/v1/oauth/token" {
		t.Errorf("aud = %v, want the token endpoint", claims["aud"])
	}
	if claims["jti"] == nil || claims["jti"] == "" {
		t.Error("jti is required and must be present")
	}

	iat, expClaim := claims["iat"].(float64), claims["exp"].(float64)
	if iat != float64(now.Unix()) {
		t.Errorf("iat = %v, want %v", iat, now.Unix())
	}
	// The server rejects exp − iat > 5 minutes.
	if lifetime := expClaim - iat; lifetime <= 0 || lifetime > 300 {
		t.Errorf("exp − iat = %v seconds, want (0, 300]", lifetime)
	}
}

// jti must not be clock-derived: two applies in the same second would collide
// and the second would be refused by the replay cache.
func TestSignAssertion_JTIIsUniquePerCall(t *testing.T) {
	_, pemStr := newTestKey(t)
	priv, err := parsePrivatePEM(pemStr)
	if err != nil {
		t.Fatalf("parsePrivatePEM: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()

	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		tok, err := signAssertion(priv, "k", "u", "aud", now)
		if err != nil {
			t.Fatalf("signAssertion: %v", err)
		}
		jti, _ := decodeSegment(t, strings.Split(tok, ".")[1])["jti"].(string)
		if seen[jti] {
			t.Fatalf("duplicate jti %q at the same timestamp — replays would be refused", jti)
		}
		seen[jti] = true
	}
}

func TestParsePrivatePEM_Errors(t *testing.T) {
	t.Run("not PEM at all", func(t *testing.T) {
		_, err := parsePrivatePEM("definitely-not-pem")
		if err == nil || !strings.Contains(err.Error(), "not valid PEM") {
			t.Fatalf("want a PEM error mentioning HCL quoting, got %v", err)
		}
	})

	t.Run("public key pasted by mistake", func(t *testing.T) {
		pub, _ := newTestKey(t)
		der, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			t.Fatalf("marshal pkix: %v", err)
		}
		pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
		_, err = parsePrivatePEM(pubPEM)
		if err == nil || !strings.Contains(err.Error(), "wrong half") {
			t.Fatalf("want the wrong-half message, got %v", err)
		}
	})

	t.Run("valid PEM round-trips", func(t *testing.T) {
		_, pemStr := newTestKey(t)
		if _, err := parsePrivatePEM(pemStr); err != nil {
			t.Fatalf("valid key rejected: %v", err)
		}
	})
}

func TestServiceAccountTokenSource_MintsAndCaches(t *testing.T) {
	_, pemStr := newTestKey(t)

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if got := r.FormValue("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q", got)
		}
		if got := r.FormValue("client_assertion_type"); got != clientAssertionType {
			t.Errorf("client_assertion_type = %q", got)
		}
		if r.FormValue("client_assertion") == "" {
			t.Error("client_assertion missing")
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", ct)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "minted-token",
			"token_type":   "Bearer",
			"expires_in":   900,
		})
	}))
	defer srv.Close()

	ts, err := NewServiceAccountTokenSource("u", "k", pemStr, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewServiceAccountTokenSource: %v", err)
	}

	for i := 0; i < 3; i++ {
		tok, err := ts.Token(context.Background())
		if err != nil {
			t.Fatalf("Token: %v", err)
		}
		if tok != "minted-token" {
			t.Fatalf("token = %q", tok)
		}
	}
	if calls != 1 {
		t.Errorf("token endpoint called %d times, want 1 — the token must be cached in memory", calls)
	}
}

// A token about to expire must be re-minted. Without the skew, a long apply
// fails halfway through with resources already created.
func TestServiceAccountTokenSource_RefreshesBeforeExpiry(t *testing.T) {
	_, pemStr := newTestKey(t)

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok", "token_type": "Bearer", "expires_in": 90,
		})
	}))
	defer srv.Close()

	ts, err := NewServiceAccountTokenSource("u", "k", pemStr, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewServiceAccountTokenSource: %v", err)
	}

	base := time.Now()
	sa := ts.(*serviceAccountSource)
	sa.now = func() time.Time { return base }

	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	// 45s in: still 45s of life left, which is inside the 60s skew.
	sa.now = func() time.Time { return base.Add(45 * time.Second) }
	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 — a token inside the refresh skew must be re-minted", calls)
	}
}

// A response with no expires_in must not be cached forever, or the provider
// starts 401ing permanently once it silently lapses.
func TestServiceAccountTokenSource_MissingExpiresInIsNotEternal(t *testing.T) {
	_, pemStr := newTestKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok"})
	}))
	defer srv.Close()

	ts, err := NewServiceAccountTokenSource("u", "k", pemStr, srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewServiceAccountTokenSource: %v", err)
	}
	if _, err := ts.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	sa := ts.(*serviceAccountSource)
	if sa.expires.After(time.Now().Add(10 * time.Minute)) {
		t.Errorf("undated token cached until %v — far too long", sa.expires)
	}
}

// The OAuth error envelope carries the actionable part. `access_denied` in
// particular must not read as a credential problem: the plan calls out that
// sending an operator to rotate a working key wastes an afternoon.
func TestServiceAccountTokenSource_ErrorMessages(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantAny  []string
		wantNone []string
	}{
		{
			name:    "invalid_client points at the credential",
			status:  http.StatusUnauthorized,
			body:    `{"error":"invalid_client","error_description":"client_assertion did not match a known active credential"}`,
			wantAny: []string{"user_id", "kid", "private_key_pem"},
		},
		{
			name:     "access_denied says do not rotate",
			status:   http.StatusForbidden,
			body:     `{"error":"access_denied","error_description":"refused by a conditional-access policy (ip_not_trusted)"}`,
			wantAny:  []string{"policy", "do not rotate"},
			wantNone: []string{"Check that user_id"},
		},
		{
			name:    "a non-OAuth body still surfaces something",
			status:  http.StatusBadGateway,
			body:    `<html>gateway</html>`,
			wantAny: []string{"502"},
		},
	}

	_, pemStr := newTestKey(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			ts, err := NewServiceAccountTokenSource("u", "k", pemStr, srv.URL, srv.Client())
			if err != nil {
				t.Fatalf("NewServiceAccountTokenSource: %v", err)
			}
			_, err = ts.Token(context.Background())
			if err == nil {
				t.Fatal("want an error")
			}
			for _, want := range tc.wantAny {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message missing %q: %s", want, err)
				}
			}
			for _, none := range tc.wantNone {
				if strings.Contains(err.Error(), none) {
					t.Errorf("message should not contain %q: %s", none, err)
				}
			}
		})
	}
}

func TestStaticTokenSource(t *testing.T) {
	tok, err := NewStaticTokenSource("pat").Token(context.Background())
	if err != nil || tok != "pat" {
		t.Fatalf("got %q, %v", tok, err)
	}
	if _, err := NewStaticTokenSource("").Token(context.Background()); err == nil {
		t.Error("empty token should error rather than send an unauthenticated request")
	}
}
