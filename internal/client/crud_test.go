package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const testID = "11111111-1111-1111-1111-111111111111"

// recording captures what the client actually put on the wire.
//
// Path and EscapedPath differ in exactly the case this file cares about: Go's
// server decodes percent-escapes before filling URL.Path, so a correctly
// escaped id reads back as though it were never escaped. EscapedPath is the
// wire form, and it is the only one that can show whether escaping happened.
type recording struct {
	Method      string
	Path        string
	EscapedPath string
}

// recorder returns a client whose server records the request it was called
// with, and answers every request with a minimal object.
func recorder(t *testing.T) (*Client, *recording, func()) {
	t.Helper()
	var rec recording
	cl, closeFn := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		rec = recording{Method: r.Method, Path: r.URL.Path, EscapedPath: r.URL.EscapedPath()}
		_, _ = w.Write([]byte(`{"id":"srv"}`))
	})
	return cl, &rec, closeFn
}

// crudCalls is every CRUD wrapper in the client, with the route each addresses.
//
// These wrappers are one-liners over Do, which makes them look too trivial to
// test. What they actually encode is the route — and a wrong route is not a
// compile error, it is a 404 at apply time that reads to a practitioner like
// somebody deleted the resource out from under Terraform. A copy-paste between
// two resource families is the obvious way to introduce one, so this table is
// exhaustive rather than representative.
//
// Each call takes the id so the same table drives both the route assertions and
// the escaping assertions below. Create calls ignore it.
var crudCalls = []struct {
	name       string
	call       func(*Client, string) error
	wantMethod string
	wantPath   string
	byID       bool
}{
	{"CreateAgent", func(c *Client, _ string) error {
		_, err := c.CreateAgent(context.Background(), AgentInput{})
		return err
	}, http.MethodPost, "/v1/agent-platform/agents", false},
	{"GetAgent", func(c *Client, id string) error {
		_, err := c.GetAgent(context.Background(), id)
		return err
	}, http.MethodGet, "/v1/agent-platform/agents/" + testID, true},
	{"UpdateAgent", func(c *Client, id string) error {
		_, err := c.UpdateAgent(context.Background(), id, AgentInput{})
		return err
	}, http.MethodPatch, "/v1/agent-platform/agents/" + testID, true},
	{"DeleteAgent", func(c *Client, id string) error {
		return c.DeleteAgent(context.Background(), id)
	}, http.MethodDelete, "/v1/agent-platform/agents/" + testID, true},

	{"CreateWorkflow", func(c *Client, _ string) error {
		_, err := c.CreateWorkflow(context.Background(), WorkflowInput{})
		return err
	}, http.MethodPost, "/v1/agent-platform/workflows", false},
	{"GetWorkflow", func(c *Client, id string) error {
		_, err := c.GetWorkflow(context.Background(), id)
		return err
	}, http.MethodGet, "/v1/agent-platform/workflows/" + testID, true},
	{"UpdateWorkflow", func(c *Client, id string) error {
		_, err := c.UpdateWorkflow(context.Background(), id, WorkflowInput{})
		return err
	}, http.MethodPatch, "/v1/agent-platform/workflows/" + testID, true},
	{"DeleteWorkflow", func(c *Client, id string) error {
		return c.DeleteWorkflow(context.Background(), id)
	}, http.MethodDelete, "/v1/agent-platform/workflows/" + testID, true},

	{"CreateContextProvider", func(c *Client, _ string) error {
		_, err := c.CreateContextProvider(context.Background(), ContextProviderInput{})
		return err
	}, http.MethodPost, "/v1/context/parent-handles", false},
	{"GetContextProvider", func(c *Client, id string) error {
		_, err := c.GetContextProvider(context.Background(), id)
		return err
	}, http.MethodGet, "/v1/context/parent-handles/" + testID, true},
	{"UpdateContextProvider", func(c *Client, id string) error {
		_, err := c.UpdateContextProvider(context.Background(), id, ContextProviderInput{})
		return err
	}, http.MethodPatch, "/v1/context/parent-handles/" + testID, true},
	// Retire, not Delete: the route soft-deletes. The verb is still DELETE.
	{"RetireContextProvider", func(c *Client, id string) error {
		return c.RetireContextProvider(context.Background(), id)
	}, http.MethodDelete, "/v1/context/parent-handles/" + testID, true},

	{"CreateMCPGateway", func(c *Client, _ string) error {
		_, err := c.CreateMCPGateway(context.Background(), MCPGatewayCreate{})
		return err
	}, http.MethodPost, "/v1/agent-platform/mcp-gateways", false},
	{"GetMCPGateway", func(c *Client, id string) error {
		_, err := c.GetMCPGateway(context.Background(), id)
		return err
	}, http.MethodGet, "/v1/agent-platform/mcp-gateways/" + testID, true},
	{"UpdateMCPGateway", func(c *Client, id string) error {
		_, err := c.UpdateMCPGateway(context.Background(), id, MCPGatewayUpdate{})
		return err
	}, http.MethodPatch, "/v1/agent-platform/mcp-gateways/" + testID, true},
	{"DeleteMCPGateway", func(c *Client, id string) error {
		return c.DeleteMCPGateway(context.Background(), id)
	}, http.MethodDelete, "/v1/agent-platform/mcp-gateways/" + testID, true},

	// The MCP tool rides the shared tools catalog, NOT an mcp-tools route.
	{"CreateMCPTool", func(c *Client, _ string) error {
		_, err := c.CreateMCPTool(context.Background(), MCPToolInput{})
		return err
	}, http.MethodPost, "/v1/agent-platform/tools", false},
	{"GetMCPTool", func(c *Client, id string) error {
		_, err := c.GetMCPTool(context.Background(), id)
		return err
	}, http.MethodGet, "/v1/agent-platform/tools/" + testID, true},
	{"UpdateMCPTool", func(c *Client, id string) error {
		_, err := c.UpdateMCPTool(context.Background(), id, MCPToolInput{})
		return err
	}, http.MethodPatch, "/v1/agent-platform/tools/" + testID, true},
	{"DeleteMCPTool", func(c *Client, id string) error {
		return c.DeleteMCPTool(context.Background(), id)
	}, http.MethodDelete, "/v1/agent-platform/tools/" + testID, true},
}

func TestCRUD_SendsTheRightMethodAndPath(t *testing.T) {
	for _, tc := range crudCalls {
		t.Run(tc.name, func(t *testing.T) {
			c, rec, done := recorder(t)
			defer done()

			if err := tc.call(c, testID); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if rec.Method != tc.wantMethod {
				t.Errorf("method = %s, want %s", rec.Method, tc.wantMethod)
			}
			if rec.Path != tc.wantPath {
				t.Errorf("path = %s, want %s", rec.Path, tc.wantPath)
			}
		})
	}
}

// Every update must PATCH. An update that silently used POST would create a
// duplicate row rather than modify the existing one. Asserted apart from the
// table so a failure names the verb rather than one resource.
func TestCRUD_UpdatesUsePatchNotPost(t *testing.T) {
	var seen int
	for _, tc := range crudCalls {
		if !strings.HasPrefix(tc.name, "Update") {
			continue
		}
		seen++
		if tc.wantMethod != http.MethodPatch {
			t.Errorf("%s expects %s — every update must PATCH", tc.name, tc.wantMethod)
		}
	}
	if seen != 5 {
		t.Errorf("checked %d update calls, want 5 — a resource lost its update", seen)
	}
}

// The id reaching the path builders comes from `terraform import`, which is raw
// operator input. Escaping is what stops it addressing a different route, and
// every builder calls url.PathEscape for that reason. Driving the real wrappers
// (rather than the builders directly) also proves each wrapper actually uses
// its builder instead of concatenating.
func TestCRUD_EscapesTheIDSoImportCannotAlterTheRoute(t *testing.T) {
	const evil = "../../../v1/keys"

	for _, tc := range crudCalls {
		if !tc.byID {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			c, rec, done := recorder(t)
			defer done()

			if err := tc.call(c, evil); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			base := strings.TrimSuffix(tc.wantPath, "/"+testID)
			// The wire form must keep the traversal inside one path segment.
			if want := base + "/" + url.PathEscape(evil); rec.EscapedPath != want {
				t.Errorf("wire path = %s, want %s", rec.EscapedPath, want)
			}
			if strings.Contains(rec.EscapedPath, "/../") {
				t.Errorf("wire path = %s — traversal survived escaping", rec.EscapedPath)
			}
		})
	}
}

// A 404 from a Get must arrive as the typed signal Read turns into "remove from
// state". Degraded to a generic error, Terraform would fail the plan instead of
// offering to recreate a resource somebody deleted in the console.
func TestCRUD_GetNotFoundStaysTyped(t *testing.T) {
	for _, tc := range crudCalls {
		if !strings.HasPrefix(tc.name, "Get") {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			c, closeFn := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"Not Found","message":"no such row"}`))
			})
			defer closeFn()

			err := tc.call(c, testID)
			if err == nil {
				t.Fatal("want an error on 404")
			}
			if !IsNotFound(err) {
				t.Errorf("IsNotFound = false for %v — Read cannot tell deleted from broken", err)
			}
		})
	}
}

// Create and Update must decode the server's response rather than echo the
// input back. The server assigns the id and normalizes fields, so a wrapper
// that dropped the body would leave Terraform storing what it sent instead of
// what exists.
func TestCRUD_DecodesTheServerResponse(t *testing.T) {
	c, _, done := recorder(t) // every response is {"id":"srv"}
	defer done()

	a, err := c.CreateAgent(context.Background(), AgentInput{})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if a.ID != "srv" {
		t.Errorf("agent id = %q, want the server's %q", a.ID, "srv")
	}

	w, err := c.UpdateWorkflow(context.Background(), testID, WorkflowInput{})
	if err != nil {
		t.Fatalf("UpdateWorkflow: %v", err)
	}
	if w.ID != "srv" {
		t.Errorf("workflow id = %q, want the server's %q", w.ID, "srv")
	}
}
