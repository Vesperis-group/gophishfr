# CLAUDE.md — Permanent working rules for GophishFR

These rules are **binding for every AI agent and every human** working in this
repository. They are not suggestions. If a rule cannot be satisfied, **stop and
report the blocker** — never work around it.

GophishFR is a security tool derived from [Gophish](https://github.com/gophish/gophish).
It sends email, renders untrusted HTML, exposes an admin interface and an API,
and handles credentials. Mistakes here have real security consequences.

---

## 1. Git workflow

- **Never work directly on `main`.** No direct commit, no direct push.
- **One feature / fix / migration = one branch = one pull request.**
- Before creating **any** branch:

  ```bash
  git switch main
  git pull --ff-only origin main
  git status --short          # must be empty
  ```

- Branch naming: `feat/`, `fix/`, `security/`, `ci/`, `build/`, `chore/`,
  `docs/`, `test/`, `refactor/` + short kebab-case name.
- Forbidden on `main`: `git push --force`, `git push --force-with-lease`,
  `git reset --hard` onto remote history, direct commits, direct pushes.
- After every merge, return to `main` and `git pull --ff-only origin main`
  **before** starting the next PR.

## 2. Remotes

```text
origin    = Vesperis-group/gophishfr     (our repository — push here)
upstream  = gophish/gophish              (read-only — NEVER push)
```

Verify `origin` is **not** `gophish/gophish` before every push. Never merge
`upstream` directly into `main`; always go through `chore/sync-upstream-YYYYMMDD`
and a PR. See [docs/UPSTREAM_SYNC.md](docs/UPSTREAM_SYNC.md).

## 3. Commits

- **Every commit must be cryptographically signed** (`git commit -S`).
- Verify the signature after committing:

  ```bash
  git log -1 --show-signature
  ```

- If signing fails: **stop for that commit.** Never fall back to an unsigned
  commit, and never disable the signature requirement.
- Use [Conventional Commits](https://www.conventionalcommits.org/):
  `feat:`, `fix:`, `security:`, `ci:`, `build:`, `test:`, `refactor:`,
  `docs:`, `chore:`.
- Explain **why** in the commit body, not just what.

## 4. Pull requests

A PR is not done until **all** of these hold:

- [ ] CI fully green — no red test, no unexplained red security check
- [ ] No unresolved review conversation
- [ ] No merge conflict
- [ ] Every commit signed and verified
- [ ] Diff self-reviewed and the review documented in the PR description

Never fabricate or simulate a GitHub approval. With a single human maintainer,
a **documented formal self-review of the diff** is the minimum bar.

Prefer a merge strategy that preserves the individual signed commits.

Keep PRs incremental. Never hide a major migration inside a
"dependency update" PR — risky migrations get their own PR.

## 5. Security

- **No secret, token, password, API key or SMTP credential in the repository** —
  not in code, tests, fixtures, `config.json`, Dockerfiles, workflows, docs or
  examples. Examples must be obviously fake.
- Never log secrets or personal data.
- Validate all untrusted input; encode output for its context.
- Enforce access control on the backend. Never trust the frontend.
- API errors must not leak stack traces, SQL or internal details.
- Never roll your own cryptography.
- Never weaken or disable an existing security control to "make it work".
- Never bypass a scanner finding without a written, narrow, justified
  suppression. **No blanket ignores, no `|| true` to hide failures.**

## 6. Tests and quality gates

- The full local gate must pass before pushing:

  ```bash
  ./scripts/verify.sh
  ```

  > **Bootstrap note.** `scripts/verify.sh` and part of the CI described in
  > this file are being added incrementally by the bootstrap PR sequence. Until
  > it exists, run `gofmt -l .`, `go vet ./...` and `go test ./...` by hand.
  > Remove this note once the script is in place.

- Never claim a check was run if it was not.
- Behaviour changes require tests. Bug fixes require a regression test.
- Security, auth, session and parser code is **high priority** for tests.
- Test coverage uses a **ratchet**: know the baseline, never degrade it without
  justification.
- Tests must never be able to send real email to the Internet. Use a local fake
  SMTP server and synthetic fixtures.

## 7. Dependencies

- No new dependency without justifying: the need, the alternative considered,
  the security impact, the maintenance impact.
- Pin versions. Never use floating references (`latest`, `@main`, `@master`).
- Keep and update lockfiles. Never delete `go.sum` or the frontend lockfile.
- Installs in CI and releases must be deterministic and immutable.

## 8. GitHub Actions

- **Every third-party action must be pinned to a full commit SHA**, with the
  human-readable version in a trailing comment:

  ```yaml
  uses: actions/checkout@<FULL_40_CHAR_SHA> # v4.2.2
  ```

- Every workflow declares minimal `permissions:` (`contents: read` by default),
  elevating only in the specific job that needs it.
- Every workflow declares `timeout-minutes`, and `concurrency` where relevant.
- Use `persist-credentials: false` on checkout unless a documented need exists.
- Avoid `pull_request_target`. If truly unavoidable, it requires a written
  security analysis first.
- Never expose secrets to untrusted pull requests.
- `.github/workflows/**` is security-sensitive code: it is linted by
  `actionlint` and `zizmor` like any other source.

## 9. Behaviour and compatibility

- Do not change business behaviour without saying so explicitly and adding a test.
- Do not break backwards compatibility without validation.
- Database migrations must be explicit, versioned and reversible where possible.
- Preserve upstream attribution: `LICENSE`, `NOTICE`, copyright notices.

## 10. Never bypass

Stop and report instead of working around:

- a commit signature failure
- an ambiguous or unexpected git remote
- a missing GitHub permission
- a genuinely failing test
- a vulnerability you do not understand
- a destructive migration
- any risk of data loss
