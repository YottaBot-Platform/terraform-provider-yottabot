package client

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// This file holds the two transport-level properties every request in this
// package depends on, kept together because both are defences against a
// *server* that misbehaves — which, for a Terraform provider, is not a
// hypothetical: `endpoint` is practitioner-supplied and frequently points at
// something in front of the estate (a proxy, an ingress, a tunnel) rather than
// at the estate itself.

// maxRedirects caps a redirect chain. Go's default is 10; this only ever
// applies to same-origin hops, since the policy below refuses the rest.
const maxRedirects = 5

// Response size caps.
//
// These exist so a hostile or simply broken endpoint cannot exhaust the
// provider's memory: io.ReadAll on a network body is unbounded, and Terraform
// runs this process on a CI worker alongside the rest of an apply.
//
// Both are enforced as an ERROR, never as a silent truncation. A cap that
// quietly returns the first N bytes turns an oversized response into a JSON
// decode failure — or worse, into a successful decode of a prefix — and the
// real cause is then invisible.
const (
	// maxAPIResponseBytes is generous on purpose: a workflow graph or a policy
	// document is the largest thing this API returns, and refusing a legitimate
	// one would be a worse bug than the memory it guards.
	maxAPIResponseBytes = 8 << 20 // 8 MiB
	// maxTokenResponseBytes covers an OAuth token response, which is a handful
	// of JSON fields. Anything approaching this is not a token response.
	maxTokenResponseBytes = 1 << 20 // 1 MiB
)

// withRedirectPolicy returns hc with a same-origin redirect policy attached,
// leaving the caller's client untouched (the copy shares the Transport, which
// is the connection pool — that sharing is intended).
//
// A policy already set by the caller wins: a test that wants to exercise
// redirect handling must be able to.
func withRedirectPolicy(hc *http.Client) *http.Client {
	if hc == nil || hc.CheckRedirect != nil {
		return hc
	}
	cp := *hc
	cp.CheckRedirect = sameOriginRedirect
	return &cp
}

// sameOriginRedirect refuses any redirect that leaves the origin the request
// was aimed at.
//
// Go's default policy is not enough here, and both gaps were verified against
// the stdlib rather than assumed:
//
//   - Authorization is stripped only when the redirect crosses to a different
//     DOMAIN. The comparison ignores the scheme, so https://host → http://host
//     keeps the header and puts the bearer token on the wire in the clear.
//   - A 307/308 redirect REPLAYS the request body at the new host regardless of
//     domain. The token endpoint's body is the Ed25519-signed client assertion,
//     so a redirect there hands signed credential material to whoever answers.
//
// Same-origin hops are still allowed because they are ordinary (trailing-slash
// normalisation, a canonical path). Everything else is an error, which surfaces
// to the practitioner instead of silently succeeding somewhere else.
func sameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects from %s", maxRedirects, origin(via[0]))
	}
	from := via[0].URL
	if strings.EqualFold(req.URL.Scheme, from.Scheme) && strings.EqualFold(req.URL.Host, from.Host) {
		return nil
	}
	return fmt.Errorf(
		"refusing to follow a redirect from %s to %s://%s: the request carries a credential, "+
			"and following it would disclose that credential to a host — or over a scheme — "+
			"you did not configure. Point `endpoint` (or `token_url`) at the final address instead",
		origin(via[0]), req.URL.Scheme, req.URL.Host)
}

func origin(req *http.Request) string {
	return req.URL.Scheme + "://" + req.URL.Host
}

// readLimited reads at most max bytes and reports anything larger as an error.
//
// It deliberately reads max+1: that is what distinguishes "exactly at the cap"
// from "over it", and without it the only way to detect an oversized body is to
// notice the truncation downstream, which is exactly the failure this prevents.
func readLimited(r io.Reader, max int64, what string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf(
			"%s is larger than the %d-byte limit this provider will read. "+
				"Nothing was decoded — a response this size is a misconfigured endpoint "+
				"(an HTML error page, or a proxy returning something other than the API) "+
				"rather than a resource",
			what, max)
	}
	return b, nil
}
