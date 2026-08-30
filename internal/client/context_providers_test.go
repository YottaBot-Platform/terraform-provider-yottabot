package client

import "testing"

// The retired-row collision is the failure a practitioner is most likely to
// hit with this resource, and the server's message names only a constraint —
// DELETE soft-retires, so the row survives holding the unique key. The
// provider has to recognise the refusal to say anything useful about it.
func TestIsRetiredDuplicate(t *testing.T) {
	dup := newAPIError("POST", parentHandlesPath, 400,
		[]byte(`{"error":"Bad Request","message":"unique constraint \"cx_parent_handles_account_id_type_external_id_key\" rejected the row"}`))
	if !IsRetiredDuplicate(dup) {
		t.Error("the unique-constraint refusal was not recognised")
	}

	// A different table's unique violation is somebody else's problem.
	otherTable := newAPIError("POST", "/v1/agent-platform/agents", 400,
		[]byte(`{"error":"Bad Request","message":"unique constraint \"users_email_account_uq\" rejected the row"}`))
	if IsRetiredDuplicate(otherTable) {
		t.Error("another table's unique violation was misread as a retired Context provider")
	}

	missingField := newAPIError("POST", parentHandlesPath, 400,
		[]byte(`{"error":"Bad Request","message":"type, external_id, display_name, discoverer are required"}`))
	if IsRetiredDuplicate(missingField) {
		t.Error("an unrelated 400 was misread as a duplicate")
	}

	notFound := newAPIError("GET", parentHandlesPath, 404, []byte(`{"error":"Not Found","message":"not found"}`))
	if IsRetiredDuplicate(notFound) {
		t.Error("a 404 was misread as a duplicate")
	}
}
