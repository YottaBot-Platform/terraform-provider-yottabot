// Package client is the typed REST client the YottaBot Terraform provider uses
// to reach the platform `/v1` API.
//
// It is a small, provider-owned client rather than a reuse of an existing
// internal one. Go's `internal` boundary forbids importing across that
// module, and shelling out to a CLI would make Terraform depend on an
// external binary that HCP Terraform would not have.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Version is stamped into X-Yotta-Client-Version so the platform can return a
// deprecation hint when the provider is too old. Overridden at release time.
var Version = "dev"

// Client issues authenticated JSON requests against one estate.
type Client struct {
	endpoint string
	tokens   TokenSource
	http     *http.Client
}

// New builds a client. endpoint is the YottaBot base URL; paths passed to the
// verb methods are absolute `/v1/...` routes, which the deployment routes to
// the owning service.
func New(endpoint string, tokens TokenSource, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{
		endpoint: strings.TrimRight(endpoint, "/"),
		tokens:   tokens,
		http:     hc,
	}
}

// APIError is a non-2xx response. It keeps the status so callers can branch on
// 404 (the delete-outside-Terraform case every resource's Read must handle)
// without string matching.
type APIError struct {
	Status  int
	Method  string
	Path    string
	Message string
	Body    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, e.Message)
	}
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, e.Body)
}

// asAPIError is errors.As specialised to *APIError, so sibling files can
// inspect a typed error without each importing errors.
func asAPIError(err error, target **APIError) bool {
	return errors.As(err, target)
}

// IsNotFound reports a 404 — the signal a resource was deleted outside
// Terraform, which Read must translate into removing it from state rather than
// erroring.
func IsNotFound(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// IsForbidden reports a 403.
func IsForbidden(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusForbidden
}

// IsUnauthorized reports a 401.
func IsUnauthorized(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusUnauthorized
}

// Do issues one request and decodes a 2xx JSON body into out (which may be nil
// for calls with no useful response, such as DELETE).
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	if c.endpoint == "" {
		return fmt.Errorf("no endpoint configured")
	}

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, rdr)
	if err != nil {
		return err
	}
	if c.tokens != nil {
		tok, err := c.tokens.Token(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("X-Yotta-Client-Version", Version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s %s: read response: %w", method, path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return newAPIError(method, path, resp.StatusCode, respBody)
	}
	if out == nil || len(bytes.TrimSpace(respBody)) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", method, path, err)
	}
	return nil
}

func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPost, path, body, out)
}

func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPatch, path, body, out)
}

func (c *Client) Delete(ctx context.Context, path string) error {
	return c.Do(ctx, http.MethodDelete, path, nil, nil)
}

// newAPIError extracts the platform's error shape so a practitioner sees the
// server's own words instead of a status code.
//
// Two response shapes are in play and both are handled: the plain
// {"error","message"} envelope, and the workflows validation envelope, which
// adds an `errors` LIST. Flattening that list matters — workflow create/update
// returns every problem at once precisely so a practitioner fixes them in one
// edit, and dropping the list here would throw that away at the last hop.
func newAPIError(method, path string, status int, body []byte) error {
	e := &APIError{
		Status: status, Method: method, Path: path,
		Body: strings.TrimSpace(string(body)),
	}

	var env struct {
		Error   string   `json:"error"`
		Message string   `json:"message"`
		Errors  []string `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		switch {
		case len(env.Errors) > 0:
			e.Message = strings.Join(env.Errors, "; ")
		case env.Message != "":
			e.Message = env.Message
		case env.Error != "":
			e.Message = env.Error
		}
	}

	// The permission story is the one the plan flags as a likely support
	// burden: agent create needs `users:write` on top of `agents:write`, so a
	// bare 403 sends people to check the wrong permission.
	if status == http.StatusForbidden {
		e.Message = strings.TrimSpace(e.Message + " — the service account is missing a permission for this route. " +
			"Note that creating an agent also requires `users:write`, because it mints the linked kind='agent' user.")
	}
	return e
}
