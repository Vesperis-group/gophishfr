# GitHub repository protection

`CODEOWNERS` alone protects nothing. This document records the repository-level
controls GophishFR relies on, why each one is set, and how to reproduce them.

Everything here is applied with `gh api` so it is reproducible and auditable.

Repository: `Vesperis-group/gophishfr`

---

## 1. Constraint: a single human maintainer

GophishFR currently has **one human maintainer**. GitHub does not let you
approve your own pull request, so a ruleset requiring
`required_approving_review_count >= 1` would make `main` **impossible to merge
into**.

We therefore deliberately **do not** require approvals, and compensate with
controls that a single maintainer *can* satisfy:

| Requirement | Status | Rationale |
|---|---|---|
| Changes must go through a PR | **enforced** | No direct commit on `main` |
| Conversations must be resolved | **enforced** | Nothing silently ignored |
| Commits must be signed | **enforced** | Provenance of every change |
| Force-push to `main` | **blocked** | History is append-only |
| Deleting `main` | **blocked** | — |
| Required status checks | **enforced** (see §3) | CI is the real gate |
| Approving review | **not required** | Impossible with one maintainer |
| Code Owner review | **not required** | Same reason |

The gap is documented on purpose: **CI and the documented self-review in the PR
description replace peer review.** As soon as a second maintainer joins the
`gophishfr-maintainers` team, §5 must be applied.

---

## 2. Ruleset `main`

Applied as ruleset id `20960512`, `enforcement: active`, **0 bypass actors**.

```bash
gh api repos/Vesperis-group/gophishfr/rulesets -X POST --input - <<'JSON'
{
  "name": "main",
  "target": "branch",
  "enforcement": "active",
  "conditions": {
    "ref_name": { "include": ["refs/heads/main"], "exclude": [] }
  },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    { "type": "required_signatures" },
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 0,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": false,
        "require_last_push_approval": false,
        "required_review_thread_resolution": true,
        "allowed_merge_methods": ["merge"]
      }
    }
  ]
}
JSON
```

Rule by rule:

- **`deletion`** — `main` cannot be deleted.
- **`non_fast_forward`** — blocks force-push. History cannot be rewritten,
  which is what makes signed commits meaningful.
- **`required_signatures`** — every commit reaching `main` must be signed and
  verified.
  > **Ordering matters.** This rule was enabled *after* PR #1, which imported
  > the upstream Gophish history. That history contains legitimate unsigned
  > third-party commits; enabling the rule earlier would have rejected it.
- **`pull_request`** — no direct commit on `main`.
  - `required_review_thread_resolution: true` — no unresolved conversation.
  - `dismiss_stale_reviews_on_push: true` — a new push invalidates earlier
    reviews.
  - `allowed_merge_methods: ["merge"]` — **merge commits only**. Squash and
    rebase merges rewrite commits and therefore **destroy the original GPG
    signatures**. Only a merge commit preserves the signed commits as they
    were authored.

No bypass actor is configured: the rules apply to administrators too.

### Verify

```bash
gh api repos/Vesperis-group/gophishfr/rulesets --jq '.[] | {id, name, enforcement}'
gh api repos/Vesperis-group/gophishfr/rulesets/<ID> --jq '.rules[].type'
```

---

## 3. Required status checks

Status checks are added to the ruleset once their job names are stable, so the
ruleset never references a check that no longer exists (which would block every
merge).

```bash
# Read the current ruleset, add the required_status_checks rule, PUT it back.
gh api repos/Vesperis-group/gophishfr/rulesets/<ID> -X PUT --input - <<'JSON'
{
  "rules": [
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "required_status_checks": [
          { "context": "<job name>" }
        ]
      }
    }
  ]
}
JSON
```

`strict_required_status_checks_policy: true` requires the branch to be up to
date with `main` before merging.

---

## 4. Repository settings

```bash
# Merge strategy: merge commits only, to preserve signed commits
gh api repos/Vesperis-group/gophishfr -X PATCH \
  -F allow_merge_commit=true \
  -F allow_squash_merge=false \
  -F allow_rebase_merge=false \
  -F delete_branch_on_merge=true

# Private vulnerability reporting (the channel documented in SECURITY.md)
gh api repos/Vesperis-group/gophishfr/private-vulnerability-reporting -X PUT

# Secret scanning and push protection
gh api repos/Vesperis-group/gophishfr -X PATCH --input - <<'JSON'
{
  "security_and_analysis": {
    "secret_scanning": { "status": "enabled" },
    "secret_scanning_push_protection": { "status": "enabled" }
  }
}
JSON

# Dependabot security updates
gh api repos/Vesperis-group/gophishfr/automated-security-fixes -X PUT
```

### Applied state

Verified with
`gh api repos/Vesperis-group/gophishfr --jq '.security_and_analysis'`:

| Setting | State | Why |
|---|---|---|
| `allow_squash_merge` | **false** | Squashing rewrites commits and drops GPG signatures |
| `allow_rebase_merge` | **false** | Same |
| `allow_merge_commit` | true | Only strategy that preserves signed commits |
| `delete_branch_on_merge` | true | No stale branches accumulating |
| `secret_scanning` | **enabled** | Free on public repositories |
| `secret_scanning_push_protection` | **enabled** | Blocks a leak *before* it reaches the remote |
| Private vulnerability reporting | **enabled** | Backs `SECURITY.md` |
| `dependabot_security_updates` | **enabled** | Automated fixes for known CVEs |
| `secret_scanning_non_provider_patterns` | **disabled** | See below |
| `secret_scanning_validity_checks` | disabled | Requires GitHub Advanced Security |

**Known limitation — non-provider patterns.** The API accepts the PATCH but the
setting stays `disabled` on this repository; it is not available on the current
plan. The consequence is that secret scanning only detects **known provider**
patterns (AWS, GitHub, Stripe…), and will *not* flag generic secrets such as a
raw SMTP password or a private key pasted into a config example.

This gap is compensated by [Gitleaks](https://github.com/gitleaks/gitleaks) in
CI, which does match generic patterns and runs on every pull request. Do not
treat GitHub secret scanning as sufficient on its own here.

---

## 5. When a second maintainer joins

Update the `pull_request` rule:

```json
{
  "type": "pull_request",
  "parameters": {
    "required_approving_review_count": 1,
    "require_code_owner_review": true,
    "require_last_push_approval": true,
    "dismiss_stale_reviews_on_push": true,
    "required_review_thread_resolution": true,
    "allowed_merge_methods": ["merge"]
  }
}
```

`require_code_owner_review` then makes `.github/CODEOWNERS` actually binding on
the sensitive surfaces it lists (`auth/`, `middleware/`, `controllers/`, `db/`,
`models/`, `config/`, `mailer/`, `imap/`, `webhook/`, `.github/`, dependency
manifests).

---

## 6. Rules

- **Never weaken an existing protection.** Removing a rule is a decision that
  must be justified and recorded here.
- Never add a bypass actor "just to unblock a merge". Fix the cause.
- Any change here must be reflected in this document in the same PR.
