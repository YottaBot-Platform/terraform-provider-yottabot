# Verifying release artifacts

Every release publishes platform ZIPs, a `SHA256SUMS` file, a detached GPG
signature over that file, an SBOM per archive, and GitHub build provenance.
Here is how to check them. `VERSION` below is without the leading `v`.

## 1. Checksums

```shell
shasum -a 256 -c terraform-provider-yottabot_${VERSION}_SHA256SUMS --ignore-missing
```

`--ignore-missing` lets you verify only the platforms you downloaded.

## 2. Signature over the checksums

Import the public signing key, then:

```shell
gpg --verify \
  terraform-provider-yottabot_${VERSION}_SHA256SUMS.sig \
  terraform-provider-yottabot_${VERSION}_SHA256SUMS
```

Expect `Good signature`. A `WARNING: This key is not certified with a trusted
signature` line is normal until you have signed the key locally — it means the
signature is valid but you have not asserted that the key is ours. **Check the
fingerprint against the one published with the release.**

Verifying the checksum file's signature and then the checksums covers every
artifact: the ZIPs are protected transitively.

## 3. Build provenance

```shell
gh attestation verify terraform-provider-yottabot_${VERSION}_linux_amd64.zip \
  --repo YottaBot-Platform/terraform-provider-yottabot
```

This proves the artifact was built by this repository's release workflow, which
a valid signature alone does not — a signature proves who signed, provenance
proves what built it.

## 4. What Terraform does on its own

`terraform init` verifies the provider against the Registry's checksums
automatically, and `terraform providers lock` records them in
`.terraform.lock.hcl`. Manual verification matters when you mirror the provider,
install it into an air-gapped environment, or need an audit record.

```shell
terraform providers lock \
  -platform=linux_amd64 \
  -platform=darwin_arm64
```

Commit the lock file. It is what makes a later `init` detect a substituted
artifact.

## If verification fails

Do not install the artifact. Do not retry against a mirror — a mirror
reproducing a bad artifact is not corroboration. Report it through
[SECURITY.md](SECURITY.md).
