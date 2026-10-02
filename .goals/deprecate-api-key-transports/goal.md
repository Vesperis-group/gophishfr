# Goal: Deprecate legacy API-key transports

## User Request

On branch `security/deprecate-legacy-api-key-transports`, establish one explicit,
non-breaking deprecation window for query-string and form-body API-key
authentication before their removal in `0.13.0`. Keep raw Authorization
temporarily supported but document it as deprecated without a removal version.
Make `Authorization: Bearer <token>` the sole recommended transport, publish
client migration and leaked-key rotation/log-retention guidance, and preserve
the complete authentication/session/RBAC/CSRF behavior in this PR.

The complete specification in
`pasted-text-f781b968-ffa2-4718-8b7a-2ed645a36c49.txt` is authoritative.

## Refined Goal

Change documentation, release guidance, and regression evidence—not API
authentication behavior. Query and form credentials continue to authenticate
through this release, but all canonical examples move to Bearer and clearly
announce removal in `0.13.0`. Raw Authorization remains accepted, is marked
deprecated, and has no sunset version. Operators are instructed to rotate keys
previously sent in URLs and review/delete historical application/proxy logs
under their retention policy.

## Measured Current Contracts

- Bearer, raw Authorization, query `api_key`, and form `api_key` are explicitly
  detected transports.
- Presence is distinct from value; invalid/empty explicit credentials never
  fall back to a session.
- Identical duplicates are accepted; distinct values are rejected.
- Query/form/raw are documented in `docs/API_AUTHENTICATION.md` as legacy
  compatibility transports. They are therefore public documented-legacy
  contracts despite accidental historical origins.
- No in-repository first-party/business client uses query, form, or raw;
  remaining occurrences are compatibility tests/scripts and documentation.
- External client population is unknown because shipped binaries have no
  transport telemetry.
- Query credentials are written into real GophishFR combined access logs and
  default nginx request logs. Form bodies and Authorization headers are not
  logged by those defaults.
- Query removal alone would not erase already logged tokens; client migration,
  token rotation, and historical log handling are required.
- No OpenAPI/Swagger or changelog exists. Releases are GitHub Releases with
  manually authored release notes and a repository `VERSION`.
- Current version is `0.12.1`; this PR does not bump it. Breaking removal is
  targeted at `0.13.0`.
- No stable repository-hosted absolute documentation URI or official calendar
  sunset date exists.

## Acceptance Criteria

### Authentication behavior remains unchanged

- [ ] Do not change the credential extractor, API auth middleware, verifier,
      session path, CSRF selection, RBAC, view-only, CORS, status codes, or error
      bodies.
- [ ] Valid Bearer, raw, query, and form credentials continue to authenticate.
- [ ] Invalid/empty query or form plus valid session remains rejected with no
      session fallback.
- [ ] Existing duplicate/conflict behavior remains unchanged.
- [ ] API-key verifier storage/migration/keyring/reveal-once behavior remains
      unchanged.
- [ ] No runtime warning, telemetry, tracking, transport counter, or secret
      logging is added.

### Canonical and deprecated contracts

- [ ] `Authorization: Bearer <token>` is the only recommended/canonical API-key
      transport in all first-party documentation and examples.
- [ ] Query `api_key` and form `api_key` are clearly marked deprecated now and
      announced for removal in version `0.13.0`.
- [ ] Raw `Authorization: <token>` is clearly marked deprecated legacy with
      Bearer as replacement and no fixed removal version.
- [ ] Documentation states all three remain accepted during this deprecation
      release.
- [ ] Do not claim query/form/raw were never public contracts; identify them as
      documented legacy compatibility.

### Client migration guidance

- [ ] Provide synthetic old/new query migration examples using a Bearer header.
- [ ] Provide synthetic old/new form migration examples using a Bearer header
      while preserving the business form body.
- [ ] Provide raw-to-Bearer migration guidance.
- [ ] Never include a realistic or actual token in docs/tests/log output.
- [ ] State the future `0.13.0` removal behavior:
      any query/form presence, including empty and combined with a valid session
      or Bearer/raw credential, will return API JSON 401 with no fallback.
- [ ] State raw remains accepted/deprecated after query/form removal until a
      separately announced change.

### Existing leak remediation guidance

- [ ] Explicitly state that query-string API keys may already exist in
      GophishFR access logs, nginx/reverse-proxy logs, browser/shell history,
      support traces, and copied diagnostics.
- [ ] Instruct operators to migrate clients to Bearer, rotate any key previously
      sent in query strings, and review/remove historical log copies according
      to retention/incident policy.
- [ ] Do not claim GophishFR can automatically identify affected clients or
      clean third-party/proxy logs.
- [ ] Describe form transport as lower default logging risk but deprecated to
      converge on one canonical mechanism.
- [ ] Preserve the separate backlog for query/form removal and online
      authentication rate limiting.

### Runtime signaling decision

- [ ] Add no RFC 9745 `Deprecation` header and no RFC 8594 `Sunset` header:
      they could misrepresent the `/api/*` resource itself and no official
      calendar sunset date exists.
- [ ] Add no `Link: rel="deprecation"` header because no stable published docs
      URI exists.
- [ ] Add no custom header or server warning log; documentation/release notes are
      the deliberate migration channel.
- [ ] Explicitly document this decision and rationale in review/PR evidence.
- [ ] Never emit token, full URL/query, form body, Authorization header,
      verifier, or secret-derived value.

### Release/version documentation

- [ ] Do not bump `VERSION` in this deprecation PR.
- [ ] Add a concise release-note artifact or repository-conventional release-note
      documentation suitable for inclusion in the next GitHub Release:
      query/form deprecated, removal in `0.13.0`, Bearer migration, rotation/log
      cleanup, raw deprecated with no removal version.
- [ ] State that the actual breaking removal release must update/version as
      `0.13.0` through the normal release process.
- [ ] Do not invent a calendar sunset timestamp.

### Examples, searches, and documentation gate

- [ ] Search all repository docs/examples/curl snippets. Recommended examples
      use Bearer only.
- [ ] Compatibility tests/scripts may continue using query/form/raw and are
      explicitly classified as tests rather than promoted examples.
- [ ] Add a small repository-conventional assertion/gate if useful to prevent
      canonical docs/examples from reintroducing `?api_key=`, form `api_key`, or
      raw Authorization; exempt only explicit compatibility tests and the
      deprecation/migration documentation.
- [ ] Do not build a documentation parser or add a dependency.
- [ ] State that no OpenAPI specification exists and create none.

### Tests and validation

- [ ] Re-run PR #62/#63 auth matrix proving valid query/form/raw/Bearer remain
      accepted and no-fallback/conflict/RBAC/CSRF behavior is unchanged.
- [ ] Container compatibility still proves Bearer, raw, query, form, and session
      contracts.
- [ ] Any docs assertion distinguishes compatibility tests from canonical
      examples.
- [ ] `./scripts/verify.sh`, `go test ./...`, `go test -race ./...`,
      `go vet ./...`, and `go build ./...` pass.
- [ ] Full browser and Docker regressions pass through normal gates.
- [ ] Run `govulncheck`, `gosec`, Gitleaks, actionlint, zizmor, Yarn audit,
      Retire.js, and dependency-delta checks as applicable. Add no suppression.
- [ ] `go.mod`, `go.sum`, `package.json`, and `yarn.lock` remain unchanged.
- [ ] No frontend source/generated asset change unless directly required; two
      clean builds remain reproducible.
- [ ] `git diff --check`, scope/dependency diff, formal self-review, and clean
      status pass before push.

### Scope and roadmap

- [ ] Do not remove or reject query/form/raw in this PR.
- [ ] Do not modify Bearer parsing, verifier/keyring/token format, session/CSRF,
      RBAC, rate limiting, logs, `events.details`, telemetry, or dependencies.
- [ ] Document the next breaking PR as
      `security/remove-legacy-api-key-transports` for `0.13.0`, retaining
      explicit query/form presence detection so unsupported credentials cannot
      fall back to session.
- [ ] Document the independent HIGH-priority next hardening
      `security/rate-limit-api-auth` for weak legacy tokens; do not implement it.
- [ ] Stop after merge without starting either following PR.

### Git and delivery

- [ ] Every new branch commit has Author and Committer exactly
      `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`.
- [ ] Every commit is signed and reports GOOD locally.
- [ ] No author, committer, co-author, message, trailer, or metadata refers to
      Copilot. Explicit user instruction overrides the default trailer.
- [ ] Signed Conventional Commits use Goal role markers and may include only
      `Assisted-by: Claude:*`.
- [ ] PR description documents current contracts, external population unknown,
      why bounded deprecation is preferable, canonical Bearer, query/form/raw
      status, removal target, migration examples, key rotation/log cleanup,
      runtime/RFC signaling decision, unchanged auth/session behavior,
      rate-limit follow-up, dependencies, tests and scanners.
- [ ] Explicitly state this PR does not stop query-string secrets reaching access
      logs; clients must migrate and rotate affected keys.
- [ ] Merge only after Goal Inspector PASS, independent security/code review
      PASS, all CI green, every GitHub commit Verified under `vg-kvr-grp`, no
      Copilot metadata, no conflict, and no unresolved conversation.
- [ ] After merge, switch to `main`, fast-forward `origin/main`, verify clean
      status, and stop without starting rate limiting.

## Scope Boundaries

**In scope:**
- Documentation/release-note deprecation of query/form and raw Authorization.
- Canonical Bearer migration examples and leaked-key rotation/log-retention
  guidance.
- Optional minimal docs assertion and directly related tests/review evidence.

**Out of scope:**
- Runtime authentication or header behavior changes.
- Query/form/raw removal, status changes, middleware refactor, telemetry, logs,
  rate limiting, verifier/keyring, session/CSRF/RBAC, frontend product behavior,
  dependencies, and `events.details`.

## Applicable Project Conventions

**Quality gates:**
- `./scripts/verify.sh`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build ./...`
- Normal browser/container/scanner gates.

**Commit convention:**
- Signed Conventional Commits with why-focused body and Goal role markers.
- `Assisted-by: Claude:Sonnet-4.6` / `Claude:Haiku-4.5` allowed.
- Author/Committer exactly
  `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`.
- No Copilot author, committer, co-author, trailer, message, or metadata.

**Guidelines:**
- `CLAUDE.md`, `SECURITY.md`, `CONTRIBUTING.md`,
  `.github/PULL_REQUEST_TEMPLATE.md`.
- No AGENTS/CONSTITUTION/additional guideline directories exist.

**Rules:**
- Never modify/push directly to main.
- Never place/log real tokens.
- Keep this release non-breaking and authentication behavior identical.
- No dependency or hidden scanner bypass.
- Full gates, independent reviews, green CI, formal self-review, verified
  identity/signatures, and no open thread before merge.
