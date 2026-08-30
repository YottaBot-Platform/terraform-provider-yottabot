package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTokenServer answers the OAuth token endpoint with a fixed status and body.
func newTokenServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// Error paths. The success path of every wrapper was covered by crud_test.go;
// what is exercised here is what happens when the server says no.
//
// This matters more than it looks. Each Create/Update wrapper ends in
//
//	if err := c.Post(...); err != nil { return nil, err }
//	return &out, nil
//
// and a wrapper that dropped the check would return a zero-valued struct with a
// nil error. Terraform would then write an empty resource into state and report
// success, which is the worst available outcome: no error to see, and a state
// file that disagrees with the server.

// Every CRUD call must surface a server error rather than swallowing it.
func TestCRUD_PropagatesServerErrors(t *testing.T) {
	for _, tc := range crudCalls {
		t.Run(tc.name, func(t *testing.T) {
			c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"Internal Server Error","message":"boom"}`))
			})
			defer done()

			err := tc.call(c, testID)
			if err == nil {
				t.Fatal("server error was swallowed")
			}
			if !strings.Contains(err.Error(), "boom") {
				t.Errorf("error lost the server's message: %v", err)
			}
		})
	}
}

// A 5xx must not be mistaken for the "resource is gone" signal. If it were,
// Read would remove a resource from state during an outage and the next apply
// would recreate something that already exists.
func TestCRUD_ServerErrorIsNotNotFound(t *testing.T) {
	c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message":"upstream unavailable"}`))
	})
	defer done()

	_, err := c.GetAgent(context.Background(), testID)
	if err == nil {
		t.Fatal("want an error")
	}
	if IsNotFound(err) {
		t.Error("503 reported as NotFound — Read would delete live resources from state during an outage")
	}
}

// A body that is not JSON must still produce a usable error. Gateways and load
// balancers return HTML, and the 405 an nginx returns for a route it will not
// proxy is exactly this case.
func TestDo_NonJSONErrorBodyIsStillReadable(t *testing.T) {
	c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte("<html><body>405 Not Allowed</body></html>"))
	})
	defer done()

	err := c.Get(context.Background(), "/v1/ping", nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "405") {
		t.Errorf("error does not mention the status: %v", err)
	}
}

// A 2xx carrying malformed JSON is a decode failure, not a success. Returning
// nil here would leave the caller with a zero-valued struct it believes came
// from the server.
func TestDo_MalformedSuccessBodyIsAnError(t *testing.T) {
	c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id": not json`))
	})
	defer done()

	var out struct {
		ID string `json:"id"`
	}
	err := c.Get(context.Background(), "/v1/ping", &out)
	if err == nil {
		t.Fatal("malformed body accepted as success")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error does not say it failed to decode: %v", err)
	}
}

// A transport failure — connection refused, DNS failure, timeout — must name
// the method and path, or the practitioner cannot tell which call failed.
func TestDo_TransportFailureNamesTheCall(t *testing.T) {
	c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {})
	done() // close the server first, so the request cannot connect

	err := c.Get(context.Background(), "/v1/agent-platform/agents", nil)
	if err == nil {
		t.Fatal("want a transport error")
	}
	if !strings.Contains(err.Error(), "GET") || !strings.Contains(err.Error(), "/v1/agent-platform/agents") {
		t.Errorf("transport error does not identify the call: %v", err)
	}
}

// A cancelled context must abort rather than hang, and must say so.
func TestDo_RespectsContextCancellation(t *testing.T) {
	c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {})
	defer done()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.Get(ctx, "/v1/ping", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
}

// New must supply a working client when the caller passes none, with a timeout
// — an http.Client with no timeout waits forever, which in Terraform means an
// apply that never returns and cannot be interrupted cleanly.
func TestNew_SuppliesADefaultClientWithATimeout(t *testing.T) {
	c := New("https://example.test", NewStaticTokenSource("t"), nil)
	if c.http == nil {
		t.Fatal("no http client supplied")
	}
	if c.http.Timeout == 0 {
		t.Error("default client has no timeout — an apply could hang indefinitely")
	}
}

func TestNewServiceAccountTokenSource_RejectsIncompleteCredentials(t *testing.T) {
	_, validPEM := newTestKey(t)

	cases := map[string]struct {
		userID, kid, pem, tokenURL string
		wantIn                     string
	}{
		"no user_id":     {"", "k", validPEM, "https://x.test/token", "user_id"},
		"no kid":         {"u", "", validPEM, "https://x.test/token", "kid"},
		"no token_url":   {"u", "k", validPEM, "", "token_url"},
		"malformed pem":  {"u", "k", "not a pem", "https://x.test/token", ""},
		"empty pem body": {"u", "k", "-----BEGIN PRIVATE KEY-----\n-----END PRIVATE KEY-----", "https://x.test/token", ""},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewServiceAccountTokenSource(tc.userID, tc.kid, tc.pem, tc.tokenURL, nil)
			if err == nil {
				t.Fatal("incomplete credentials accepted — the failure would surface as a confusing 401 at apply")
			}
			if tc.wantIn != "" && !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error does not name the missing field %q: %v", tc.wantIn, err)
			}
		})
	}
}

func TestNewServiceAccountTokenSource_SuppliesADefaultClient(t *testing.T) {
	_, pem := newTestKey(t)
	ts, err := NewServiceAccountTokenSource("u", "k", pem, "https://x.test/token", nil)
	if err != nil {
		t.Fatalf("valid credentials rejected: %v", err)
	}
	s, ok := ts.(*serviceAccountSource)
	if !ok {
		t.Fatalf("unexpected token source type %T", ts)
	}
	if s.http == nil || s.http.Timeout == 0 {
		t.Error("default token-source client missing or has no timeout")
	}
}

// A token endpoint that fails must produce an error naming the OAuth problem.
// Terraform surfaces this before any resource call, so it is the first thing a
// practitioner sees when their service account is wrong.
func TestServiceAccountToken_SurfacesOAuthErrors(t *testing.T) {
	_, pem := newTestKey(t)

	for name, body := range map[string]string{
		"oauth error shape": `{"error":"invalid_client","error_description":"unknown kid"}`,
		"opaque body":       `nope`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := newTokenServer(t, http.StatusBadRequest, body)
			defer srv.Close()

			ts, err := NewServiceAccountTokenSource("u", "k", pem, srv.URL, srv.Client())
			if err != nil {
				t.Fatalf("building source: %v", err)
			}
			if _, err := ts.Token(context.Background()); err == nil {
				t.Fatal("token endpoint failure was not surfaced")
			}
		})
	}
}

// IsRetiredDuplicate must recognise only the retirement collision. Treating any
// 400 as one would tell practitioners to import a row that does not exist.
func TestIsRetiredDuplicate_OnlyMatchesTheRetirementCollision(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"nil": {nil, false},
		"plain 400": {&APIError{Status: http.StatusBadRequest,
			Message: "display_name is required"}, false},
		"404":           {&APIError{Status: http.StatusNotFound, Message: "not found"}, false},
		"non-api error": {errors.New("dial tcp: connection refused"), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := IsRetiredDuplicate(tc.err); got != tc.want {
				t.Errorf("IsRetiredDuplicate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsNotFound_IgnoresNilAndForeignErrors(t *testing.T) {
	if IsNotFound(nil) {
		t.Error("nil reported as NotFound")
	}
	if IsNotFound(errors.New("some transport failure")) {
		t.Error("non-API error reported as NotFound")
	}
}

// signAssertion must refuse a key it cannot sign with rather than emitting an
// assertion the server will reject for reasons the practitioner cannot see.
func TestSignAssertion_RefusesAnInvalidKey(t *testing.T) {
	if _, err := signAssertion(nil, "kid", "user", "https://x.test/token", time.Now()); err == nil {
		t.Error("signing with a nil key produced an assertion")
	}
}
