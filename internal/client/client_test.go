package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, h http.HandlerFunc) (*Client, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	return New(srv.URL, NewStaticTokenSource("tok"), srv.Client()), srv.Close
}

func TestClient_SendsAuthAndVersionHeaders(t *testing.T) {
	var gotAuth, gotVersion, gotAccept string
	c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("X-Yotta-Client-Version")
		gotAccept = r.Header.Get("Accept")
		_, _ = w.Write([]byte(`{"id":"x"}`))
	})
	defer done()

	var out struct {
		ID string `json:"id"`
	}
	if err := c.Get(context.Background(), "/v1/agent-platform/agents/x", &out); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotVersion == "" {
		t.Error("X-Yotta-Client-Version not sent — the platform uses it for deprecation hints")
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if out.ID != "x" {
		t.Errorf("body not decoded: %+v", out)
	}
}

// The endpoint may carry a trailing slash; doubling it up produces 404s that
// look like a missing resource.
func TestClient_TrimsTrailingSlashOnEndpoint(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(srv.URL+"/", NewStaticTokenSource("t"), srv.Client())
	if err := c.Get(context.Background(), "/v1/ping", nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotPath != "/v1/ping" {
		t.Errorf("path = %q, want /v1/ping (no doubled slash)", gotPath)
	}
}

func TestClient_PostSendsJSONBody(t *testing.T) {
	var gotBody map[string]any
	var gotCT string
	c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"new"}`))
	})
	defer done()

	var out struct {
		ID string `json:"id"`
	}
	if err := c.Post(context.Background(), "/v1/agent-platform/agents",
		map[string]any{"name": "RepoAuditor"}, &out); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if gotBody["name"] != "RepoAuditor" {
		t.Errorf("body = %+v", gotBody)
	}
	if out.ID != "new" {
		t.Errorf("201 body not decoded: %+v", out)
	}
}

// DELETE commonly returns 204 with no body; decoding must not treat that as a
// failure.
func TestClient_HandlesEmptyBody(t *testing.T) {
	c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	defer done()

	if err := c.Delete(context.Background(), "/v1/agent-platform/agents/x"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

// Read must be able to tell "deleted outside Terraform" from a real failure,
// without string matching.
func TestClient_NotFoundIsTyped(t *testing.T) {
	c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Not Found","message":"agent not found"}`))
	})
	defer done()

	err := c.Get(context.Background(), "/v1/agent-platform/agents/gone", nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if !IsNotFound(err) {
		t.Errorf("IsNotFound = false for a 404: %v", err)
	}
	if IsForbidden(err) || IsUnauthorized(err) {
		t.Error("404 misclassified")
	}
	if !strings.Contains(err.Error(), "agent not found") {
		t.Errorf("server message lost: %v", err)
	}
}

// Workflow create/update returns EVERY validation problem at once. Dropping the
// list here would throw that away at the last hop.
func TestClient_FlattensTheValidationErrorList(t *testing.T) {
	c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Bad Request","message":"validation_failed","errors":[` +
			`"step[0] \"a\": unknown step type \"teleport\"",` +
			`"step[1] \"b\": conditional requires a condition"]}`))
	})
	defer done()

	err := c.Post(context.Background(), "/v1/agent-platform/workflows", map[string]any{}, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"teleport", "conditional requires"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error list not surfaced (%q missing): %v", want, err)
		}
	}
}

// A 403 must name the permission trap the plan calls out, or the practitioner
// checks `agents:write` (which they have) and gets stuck.
func TestClient_ForbiddenExplainsTheUsersWriteTrap(t *testing.T) {
	c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"Forbidden","message":"permission denied"}`))
	})
	defer done()

	err := c.Post(context.Background(), "/v1/agent-platform/agents", map[string]any{}, nil)
	if err == nil || !IsForbidden(err) {
		t.Fatalf("want a typed 403, got %v", err)
	}
	if !strings.Contains(err.Error(), "users:write") {
		t.Errorf("403 does not mention the users:write requirement: %v", err)
	}
}

// A non-JSON error body (a proxy's HTML, say) must still produce a usable
// message rather than an empty one.
func TestClient_NonJSONErrorBodyStillReadable(t *testing.T) {
	c, done := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	})
	defer done()

	err := c.Get(context.Background(), "/v1/ping", nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("status lost: %v", err)
	}
}

// A token-source failure must surface as itself, not as a confusing HTTP error
// — and must not send an unauthenticated request.
func TestClient_TokenFailureIsNotSentAsAnonymousRequest(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	c := New(srv.URL, NewStaticTokenSource(""), srv.Client())
	if err := c.Get(context.Background(), "/v1/ping", nil); err == nil {
		t.Fatal("want an error")
	}
	if called {
		t.Error("request was sent without credentials")
	}
}
