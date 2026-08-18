# Security Policy

GophishFR is a phishing simulation framework derived from
[Gophish](https://github.com/gophish/gophish). It sends email, renders
untrusted HTML, exposes an administration interface and an API, and stores
campaign data and credentials. Security reports are taken seriously.

## Reporting a vulnerability

**Report privately through GitHub Private Vulnerability Reporting:**

<https://github.com/Vesperis-group/gophishfr/security/advisories/new>

Please do **not** open a public issue, pull request or discussion for a
suspected vulnerability.

Report every vulnerability affecting GophishFR through the channel above.
GophishFR is maintained independently, and the historical Gophish maintainers
are not responsible for this code.

### What to include

- Affected version or commit SHA
- Component (admin UI, API, phishing server, IMAP monitor, mailer, webhooks, import, Docker image...)
- Impact and a realistic attack scenario
- Reproduction steps or a proof of concept
- Any suggested remediation

### What to expect

| Stage | Target |
|---|---|
| Acknowledgement | 5 business days |
| Initial assessment and severity | 10 business days |
| Fix or documented mitigation plan | Depends on severity, communicated in the assessment |

GophishFR is maintained on a best-effort basis. We will keep you informed and
will credit you in the advisory unless you prefer otherwise.

## Coordinated disclosure

Please give us reasonable time to ship a fix before public disclosure. We will
publish a GitHub Security Advisory once a fix or mitigation is available.

## Supported versions

GophishFR is in its bootstrap phase and has **no released version yet**. Only
the `main` branch is supported. This section will be updated once releases
begin.

## Scope

In scope:

- The GophishFR source code in this repository
- The build, release and supply-chain pipeline (`.github/workflows/**`)
- The published container image and release artifacts

Out of scope:

- Vulnerabilities only reachable through a deliberately insecure deployment,
  for example an admin interface exposed to the Internet without TLS
- The fact that GophishFR **is** a phishing tool. Sending simulated phishing
  email is its intended purpose, not a vulnerability.
- Findings in third-party dependencies with no exploitable path in GophishFR.
  Please report those upstream, and open a normal issue here if we should
  upgrade.

## Using GophishFR responsibly

GophishFR is intended for authorised security awareness training and
penetration testing. Only use it against systems and people you have explicit
written permission to test. Misuse may be illegal.