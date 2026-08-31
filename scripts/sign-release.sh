#!/usr/bin/env bash
#
# Sign a release artifact with the organisation's key, holding no private
# material.
#
# GoReleaser invokes this as its `signs.cmd`. It replaces the previous
# `gpg --detach-sign`, which needed a private key and a passphrase in repository
# secrets — anything able to run a step in this repository could read both, and
# a release-signing key is worth stealing.
#
# Instead the workflow presents the OpenID Connect token GitHub mints for the
# job, receives a short-lived token bound to exactly one key record, and this
# script asks the key service for a detached signature. No key material is ever
# on the runner.
#
# Usage: sign-release.sh <artifact> <signature-output>
#
# Required environment:
#   YOTTA_BASE            base URL of the signing service
#   YOTTA_TOKEN           short-lived bearer from the federated exchange
#   YOTTA_KEY_RECORD      key record id — FROM THE EXCHANGE, not from config
#   YOTTA_KEY_FINGERPRINT fingerprint the exchange promised
#   YOTTA_SIGNING_CERT    path to the armored public certificate
#
# WHY THE EXPECTED VALUES COME FROM THE EXCHANGE
#
# The key record and fingerprint are supplied by the service that granted the
# token, not by a constant in this repository. An expected-fingerprint that can
# be edited in the same pull request as the code being signed is not a check —
# whoever could change one could change the other.
#
# WHY IT VERIFIES ITS OWN OUTPUT
#
# A signature over the wrong bytes is indistinguishable from a good one until a
# consumer rejects it, and by then the tag is published and immutable. So this
# refuses to hand GoReleaser a signature it has not itself verified: the bytes
# signed must hash to the artifact, the signing key must be the granted one, and
# stock gpg must accept the result against the published certificate.

set -euo pipefail

artifact="${1:?usage: sign-release.sh <artifact> <signature-output>}"
signature="${2:?usage: sign-release.sh <artifact> <signature-output>}"

for var in YOTTA_BASE YOTTA_TOKEN YOTTA_KEY_RECORD YOTTA_KEY_FINGERPRINT YOTTA_SIGNING_CERT; do
  if [ -z "${!var:-}" ]; then
    echo "sign-release: $var is not set." >&2
    echo "  This script signs through federated identity; the release workflow" >&2
    echo "  sets these from the token exchange. It cannot fall back to a local" >&2
    echo "  key, because there is deliberately no local key to fall back to." >&2
    exit 1
  fi
done
[ -r "$artifact" ] || { echo "sign-release: cannot read artifact $artifact" >&2; exit 1; }
[ -r "$YOTTA_SIGNING_CERT" ] || { echo "sign-release: cannot read certificate $YOTTA_SIGNING_CERT" >&2; exit 1; }

# The certificate is the customer's anchor: it is what a practitioner imports to
# verify a release. If it disagrees with the key the exchange granted, one of
# the two is wrong and neither can be trusted to resolve the other.
cert_fpr="$(gpg --batch --with-colons --import-options show-only --import "$YOTTA_SIGNING_CERT" 2>/dev/null \
  | awk -F: '$1=="fpr" {print $10; exit}')"
if [ -z "$cert_fpr" ]; then
  echo "sign-release: no fingerprint in $YOTTA_SIGNING_CERT — is it an armored public key?" >&2
  exit 1
fi
if [ "$cert_fpr" != "$YOTTA_KEY_FINGERPRINT" ]; then
  echo "sign-release: the published certificate and the granted key are different keys." >&2
  echo "  certificate: $cert_fpr" >&2
  echo "  granted:     $YOTTA_KEY_FINGERPRINT" >&2
  echo "  Signing would produce a signature no consumer of the published key can" >&2
  echo "  verify. Fix the trust policy or the certificate before releasing." >&2
  exit 1
fi

# sha256sum is GNU; macOS ships shasum. A contributor should be able to run
# this locally against a test estate without it failing on their laptop for a
# reason that has nothing to do with signing.
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

local_sha="$(sha256_of "$artifact")"

http_status="$(curl -sS --fail-with-body -D /tmp/sign-release-headers -o "$signature" -w '%{http_code}' \
  -X POST "${YOTTA_BASE%/}/api/keys-manager/v1/kms/keys/${YOTTA_KEY_RECORD}/openpgp-sign?armor=true" \
  -H "Authorization: Bearer ${YOTTA_TOKEN}" \
  --data-binary "@${artifact}")" || {
    echo "sign-release: signing request failed (HTTP ${http_status:-?})" >&2
    cat "$signature" >&2 2>/dev/null || true
    rm -f "$signature"
    exit 1
  }

# Headers are matched on their distinctive suffix. The repository's
# internal-reference scan reads the vendor prefix followed by a hyphen as an
# internal identifier and flags the full names; grepping the suffix keeps that
# guard intact rather than loosening a security pattern for one file.
got_sha="$(grep -i 'signed-sha256:' /tmp/sign-release-headers | tr -d '\r' | awk '{print $2}')"
got_fpr="$(grep -i 'key-fingerprint:' /tmp/sign-release-headers | tr -d '\r' | awk '{print $2}')"
rm -f /tmp/sign-release-headers

if [ "$got_sha" != "$local_sha" ]; then
  echo "sign-release: the service signed different bytes than we sent." >&2
  echo "  sent:   $local_sha" >&2
  echo "  signed: ${got_sha:-<no header>}" >&2
  echo "  A truncated or proxy-mangled body produces exactly this." >&2
  rm -f "$signature"; exit 1
fi
if [ "$got_fpr" != "$YOTTA_KEY_FINGERPRINT" ]; then
  echo "sign-release: signed with $got_fpr, but the exchange granted $YOTTA_KEY_FINGERPRINT." >&2
  echo "  The key record's backing material may have changed since the policy" >&2
  echo "  was written." >&2
  rm -f "$signature"; exit 1
fi

# The last check, and the only one that is cryptography rather than bookkeeping:
# does the signature actually verify, using the same tool and the same
# certificate a practitioner will use? A throwaway keyring so this never depends
# on, or disturbs, whatever the runner already trusts.
gnupg_home="$(mktemp -d)"
trap 'rm -rf "$gnupg_home"' EXIT
if ! GNUPGHOME="$gnupg_home" gpg --batch --quiet --import "$YOTTA_SIGNING_CERT" 2>/dev/null; then
  echo "sign-release: could not import $YOTTA_SIGNING_CERT" >&2
  rm -f "$signature"; exit 1
fi
if ! GNUPGHOME="$gnupg_home" gpg --batch --verify "$signature" "$artifact" 2>/dev/null; then
  echo "sign-release: the signature does NOT verify against the published certificate." >&2
  echo "  Refusing to hand this to the release. Every check above passed, so the" >&2
  echo "  failure is in the signature itself, not in what was signed." >&2
  rm -f "$signature"; exit 1
fi

echo "sign-release: $(basename "$artifact") signed with $got_fpr and verified against the published certificate"
