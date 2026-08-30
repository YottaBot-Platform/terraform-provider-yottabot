package client

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// This file implements the client half of the RFC 7521/7523 JWT client
// assertion that YottaBot's machine-auth service verifies.
//
// It is written out rather than imported from the server ON PURPOSE: the
// provider is a separate module whose binary must carry a minimal dependency
// graph, and requiring the server module would pull its whole go.mod into
// version resolution. What follows is stdlib-only — crypto/ed25519,
// crypto/x509, encoding/pem — so the cost of the duplication is ~40 lines with
// no dependency at all.
//
// The verifier is the spec. If any of these change, this file is wrong:
//
//   - key type ed25519, PKCS#8, PEM block "PRIVATE KEY"  (crypto.ParsePrivatePEM)
//   - JWS header alg=EdDSA, typ=JWT, kid=<credential kid> (crypto.SignJWS)
//   - iss == sub == the credential's user_id            (RFC 7523 §3)
//   - aud names the token endpoint                       (assertionAudienceOK)
//   - jti present and unique within the exp window       (JTI replay cache)
//   - exp − iat ≤ 5 minutes                              (clientAssertionMaxLifetime)

// assertionLifetime is how long a minted assertion is valid. The server caps
// this at 5 minutes and rejects anything longer; 60s is plenty for one token
// call and keeps the replay window small.
const assertionLifetime = 60 * time.Second

// pemTypePrivateKey mirrors the block type the platform writes and parses.
const pemTypePrivateKey = "PRIVATE KEY"

// parsePrivatePEM decodes a PEM-encoded PKCS#8 Ed25519 private key.
//
// The error messages are deliberately specific: a practitioner pasting a key
// into a Terraform variable gets one of these back, and "invalid key" would
// send them looking in the wrong place. The three realistic mistakes — pasting
// the public key, pasting an RSA/EC key, and mangling the PEM in HCL quoting —
// each get their own message.
func parsePrivatePEM(s string) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode([]byte(s))
	if block == nil {
		return nil, errors.New(
			"private_key_pem is not valid PEM: expected a -----BEGIN PRIVATE KEY----- block " +
				"(in HCL use a heredoc or file(), not a quoted string, so newlines survive)")
	}
	if block.Type != pemTypePrivateKey {
		return nil, fmt.Errorf(
			"private_key_pem holds a %q block, want %q — this looks like the wrong half of the keypair",
			block.Type, pemTypePrivateKey)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("private_key_pem is not a PKCS#8 key: %w", err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf(
			"private_key_pem is a %T; the platform issues ed25519 keys only", parsed)
	}
	return priv, nil
}

// newJTI returns a random assertion id. Uniqueness is what the server's replay
// cache keys on, so this must not be derived from the clock: two applies in the
// same second would collide and the second would be refused as a replay.
func newJTI() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate jti: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// signAssertion builds the compact JWS the token endpoint expects.
func signAssertion(priv ed25519.PrivateKey, kid, userID, audience string, now time.Time) (string, error) {
	// ed25519.Sign PANICS on a key of the wrong size rather than returning an
	// error. Inside a Terraform provider a panic surfaces to the practitioner
	// as a plugin crash with no usable message, so trade it for an error here.
	// Today parsePrivatePEM makes this unreachable; the guard is for the
	// refactor that stops being true.
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("signing key is %d bytes, want %d — the credential's private key is not a valid Ed25519 key",
			len(priv), ed25519.PrivateKeySize)
	}

	jti, err := newJTI()
	if err != nil {
		return "", err
	}
	header := map[string]any{
		"alg": "EdDSA",
		"typ": "JWT",
		"kid": kid,
	}
	claims := map[string]any{
		// iss and sub are both the credential's user_id — RFC 7523 §3's
		// self-asserted profile, and the server rejects them individually.
		"iss": userID,
		"sub": userID,
		"aud": audience,
		"jti": jti,
		"iat": now.Unix(),
		"exp": now.Add(assertionLifetime).Unix(),
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshal assertion header: %w", err)
	}
	pb, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal assertion claims: %w", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." +
		base64.RawURLEncoding.EncodeToString(pb)
	sig := ed25519.Sign(priv, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
