package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The whole point of the redirect policy is that a credential never reaches a
// host the practitioner did not configure. These tests therefore assert on what
// the SECOND server received, not merely on the error — an error with the token
// already delivered would be a passing test over a failed defence.

func TestClient_RefusesCrossHostRedirect(t *testing.T) {
	var landedAuth string
	var landed bool
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		landed = true
		landedAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"id":"attacker"}`))
	}))
	defer sink.Close()

	// "localhost" and "127.0.0.1" are the same machine but different hosts as
	// far as any origin comparison is concerned, which is what makes this a
	// cross-host hop without needing DNS.
	target := strings.Replace(sink.URL, "127.0.0.1", "localhost", 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+"/landed", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	c := New(srv.URL, NewStaticTokenSource("supersecret"), srv.Client())
	err := c.Get(context.Background(), "/v1/agent-platform/agents/x", &struct{}{})
	if err == nil {
		t.Fatal("following a cross-host redirect succeeded; it must be refused")
	}
	if !strings.Contains(err.Error(), "refusing to follow a redirect") {
		t.Errorf("error does not explain the refusal: %v", err)
	}
	if landed {
		t.Errorf("the request reached the redirect target (Authorization=%q)", landedAuth)
	}
}

// The scheme downgrade is the case Go's own policy does NOT cover: it strips
// Authorization only across a different DOMAIN, and that comparison ignores the
// scheme. Without the policy in transport.go this test fails by delivering the
// bearer token to a plaintext listener.
func TestClient_RefusesRedirectToCleartext(t *testing.T) {
	var landedAuth string
	cleartext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		landedAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer cleartext.Close()

	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, cleartext.URL+"/landed", http.StatusTemporaryRedirect)
	}))
	defer secure.Close()

	c := New(secure.URL, NewStaticTokenSource("supersecret"), secure.Client())
	if err := c.Get(context.Background(), "/v1/ping", &struct{}{}); err == nil {
		t.Fatal("a redirect from https to cleartext http was followed")
	}
	if landedAuth != "" {
		t.Errorf("bearer token delivered over cleartext http: %q", landedAuth)
	}
}

// Same-origin hops stay legal — trailing-slash and canonical-path
// normalisation are ordinary server behaviour, and refusing them would break
// working estates for no security gain.
func TestClient_FollowsSameOriginRedirect(t *testing.T) {
	var reached string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/ping" {
			http.Redirect(w, r, "/v1/ping/", http.StatusTemporaryRedirect)
			return
		}
		reached = r.URL.Path
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, NewStaticTokenSource("t"), srv.Client())
	if err := c.Get(context.Background(), "/v1/ping", &struct{}{}); err != nil {
		t.Fatalf("same-origin redirect refused: %v", err)
	}
	if reached != "/v1/ping/" {
		t.Errorf("redirect not followed; reached %q", reached)
	}
}

// The token endpoint is the one request whose BODY is credential material. Go
// replays a 307/308 body at the new host even when it strips the headers, so
// without the policy the signed assertion is handed to whoever answers.
func TestTokenSource_RefusesRedirect(t *testing.T) {
	var landedBody string
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		landedBody = string(b)
		_, _ = w.Write([]byte(`{"access_token":"attacker-issued","expires_in":900}`))
	}))
	defer sink.Close()

	target := strings.Replace(sink.URL, "127.0.0.1", "localhost", 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+"/token", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	ts, err := NewServiceAccountTokenSource("user-1", "kid-1", testKeyPEM(t), srv.URL+"/token", srv.Client())
	if err != nil {
		t.Fatalf("NewServiceAccountTokenSource: %v", err)
	}
	tok, err := ts.Token(context.Background())
	if err == nil {
		t.Fatalf("token minted through a redirect: %q", tok)
	}
	if strings.Contains(landedBody, "client_assertion") {
		t.Error("the signed client assertion was replayed to the redirect target")
	}
	if tok == "attacker-issued" {
		t.Error("accepted a token from the redirect target")
	}
}

func TestReadLimited(t *testing.T) {
	t.Run("at the cap is fine", func(t *testing.T) {
		b, err := readLimited(strings.NewReader("12345"), 5, "body")
		if err != nil {
			t.Fatalf("readLimited: %v", err)
		}
		if string(b) != "12345" {
			t.Errorf("got %q, want the whole body", b)
		}
	})

	// Silent truncation is the failure mode this guards against: it turns an
	// oversized response into a decode error, or into a successfully decoded
	// PREFIX, and either way hides the real cause.
	t.Run("over the cap errors rather than truncating", func(t *testing.T) {
		_, err := readLimited(strings.NewReader("123456"), 5, "the response body")
		if err == nil {
			t.Fatal("an oversized body was accepted")
		}
		if !strings.Contains(err.Error(), "larger than the 5-byte limit") {
			t.Errorf("error does not name the limit: %v", err)
		}
	})
}

func TestClient_RefusesOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"`))
		chunk := strings.Repeat("a", 1<<20)
		for written := 0; written <= maxAPIResponseBytes; written += len(chunk) {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	c := New(srv.URL, NewStaticTokenSource("t"), srv.Client())
	var out struct {
		ID string `json:"id"`
	}
	err := c.Get(context.Background(), "/v1/agent-platform/agents/x", &out)
	if err == nil {
		t.Fatal("an unbounded response body was read to completion")
	}
	if !strings.Contains(err.Error(), "limit this provider will read") {
		t.Errorf("error does not explain the cap: %v", err)
	}
	if out.ID != "" {
		t.Errorf("decoded a truncated body into state: %+v", out)
	}
}

// testKeyPEM returns a throwaway PKCS#8 Ed25519 key in the PEM shape
// parsePrivatePEM accepts.
func testKeyPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: pemTypePrivateKey, Bytes: der}))
}
