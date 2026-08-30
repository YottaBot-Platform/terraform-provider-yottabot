# Security Policy

## Reporting a vulnerability

**Do not report security vulnerabilities through public GitHub issues,
discussions, or pull requests.**

Use GitHub's private vulnerability reporting: go to the **Security** tab of this
repository and choose **Report a vulnerability**. The report is visible only to
maintainers, and you can collaborate with us on a fix in the same place.

Please include:

- the version of the provider, Terraform CLI, and YottaBot you were running
- what an attacker gains, and what access they need to get it
- a minimal configuration or sequence that reproduces the issue
- anything you already know about scope — whether it affects state files,
  credentials in transit, or only local execution

**Do not include real credentials, tokens, private keys, or customer data in a
report.** Redact them. If a credential has been exposed, rotate it first and say
so in the report.

## What to expect

We aim to acknowledge a report within three business days and to give you an
assessment, including whether we consider it a vulnerability and a rough
remediation timeline, within ten business days. We will tell you when a fix
ships and credit you in the release notes unless you ask us not to.

## Scope

In scope: this provider's source, its released binaries, and its release and
signing pipeline.

Out of scope, and better reported through the same channel on the relevant
project: vulnerabilities in the YottaBot server, in Terraform itself, or in a
third-party dependency where the issue is not reachable through this provider.

## A note on Terraform state

Terraform state contains every attribute of every managed resource, including
values marked sensitive. That is a property of Terraform, not a defect in this
provider. Protect state accordingly — use a backend with encryption and access
control, and never commit state to a repository. This provider deliberately does
not manage credential-minting operations for the same reason.
