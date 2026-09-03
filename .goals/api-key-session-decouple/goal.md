# Goal: Decouple the SPA from API keys

## User Request

On branch `security/api-key-session-decouple`, allow first-party same-origin SPA
requests to authenticate to `/api/*` with the existing web session while
preserving every external API-key transport and RBAC contract. Remove the API
key from the global HTML/JavaScript user object and from all SPA HTTP transport,
protect unsafe session-authenticated API requests with the existing same-origin
CSRF mechanism, and ensure any explicit invalid or empty API credential can
never fall back to a valid session.

The complete specification in
`pasted-text-b2f4bd52-f35f-472b-959d-12650d25af50.txt` is authoritative.
API-key hashing, verifier storage, and reveal-once behavior remain a future PR.

## Refined Goal

Select the API authentication mechanism before validating credentials:
requests containing any explicit Authorization/query/form API credential use
API-key authentication only, while requests with no explicit credential use the
already-populated web session only. Reuse the same extraction decision for the
CSRF exemption so explicit API-key clients remain compatible and session-based
unsafe methods pass only through the existing Fetch-Metadata/Origin same-origin
protection. Switch every first-party frontend transport to same-origin cookies
and remove `api_key` from the common page state, while retaining its temporary
dedicated settings/API-management exposure for the verifier PR.

## Measured Legacy Contracts

- The outer admin chain populates session context before CSRF and API routing:
  security/context middleware -> credential-aware CSRF wrapper -> router.
- `/api/*` currently runs `RequireAPIKey`, then `EnforceViewOnly`, then
  route-specific `RequirePermission`.
- `RequireAPIKey` parses forms, obtains `r.Form.Get("api_key")`, and only when it
  is empty reads the first Authorization header and removes an exact
  `Bearer ` prefix. Raw Authorization values are also accepted.
- Net/http form semantics give POST form values precedence over query values;
  the current overall silent precedence is form > query > Authorization.
- Current code cannot distinguish an absent `api_key` from a present empty one.
- Current multiple credentials are silently resolved by legacy precedence.
- API-key lookup is parameterized `WHERE api_key = ?`, loads the Role, and sets
  request context `user` and `user_id`; the context `api_key` is unused.
- Missing or invalid API key returns API JSON 401. Permission failures preserve
  existing 403 conventions. API routes do not redirect to HTML login.
- The existing web session cookie is named `gophish`, HttpOnly, max age five
  days, and stores user ID. Session keys are regenerated on restart. Secure and
  SameSite are not explicitly set; this PR does not change cookie policy.
- `GetContext` already loads the session user and role for `/api/*`, or stores a
  nil user for an invalid/expired/absent session.
- Existing CSRF uses `filippo.io/csrf` backed by Go
  `net/http.CrossOriginProtection`: Fetch Metadata and Origin/Host trusted-origin
  checks, not a secret token. Safe methods pass. Browser cross-site/same-site
  unsafe requests are rejected; non-browser requests without browser origin
  headers remain allowed.
- `/api` is currently blanket-exempted through `csrf.UnsafeSkipCheck`.
- API middleware unconditionally preserves existing CORS/OPTIONS behavior.
- `requestJSON` already uses `credentials:"same-origin"` but adds
  `Authorization: Bearer <global user.api_key>`.
- The group CSV upload is a second direct fetch that adds the global API key.
- `templates/base.html` injects the complete API key into the global `user`
  object on every authenticated page.
- Dedicated settings currently renders the complete key and reset updates that
  field. `/api/users` and `/api/reset` also return it. These management/public
  API contracts remain temporarily unchanged.
- API keys remain plaintext `users.api_key`, one per user, in this PR.

## Acceptance Criteria

### Explicit credential extraction

- [ ] Implement one small shared extractor used by API authentication and the
      CSRF exemption decision; do not duplicate subtly different presence rules.
- [ ] Recognize all legacy explicit transports:
      exact `Authorization: Bearer <value>`, raw Authorization value, query
      `api_key`, and form `api_key`.
- [ ] Detect presence separately from value. An existing header/query/form key
      with an empty value is an explicit invalid credential, not absence.
- [ ] Preserve ordinary single-credential Bearer, raw, query, and form parsing.
- [ ] Never include credential values in source enums, errors, logs, metrics, or
      traces.
- [ ] Parse request forms once/cached using normal net/http semantics without
      changing business payloads or consuming bodies irrecoverably.

### Multiple explicit credentials

- [ ] Collect values from all present explicit sources, including repeated values
      within the same source.
- [ ] Identical duplicate explicit values are accepted as one credential.
- [ ] Distinct explicit values across form, query, repeated parameters, or
      Authorization headers are rejected as ambiguous with API authentication
      failure.
- [ ] Document this intentional hardening from silent legacy precedence; do not
      accidentally choose based on map/header order.
- [ ] A conflicting/empty/invalid explicit set never reaches session
      authentication.

### Authentication mechanism selection

- [ ] Choose the mechanism before validation:
      if an explicit credential is present, authenticate only that API
      credential; otherwise authenticate only the existing session.
- [ ] Never implement try-API-key-then-session fallback.
- [ ] Valid explicit API key plus no session succeeds as the API-key user.
- [ ] Valid explicit API key plus valid session succeeds as the API-key user,
      regardless of session identity.
- [ ] Invalid explicit API key plus valid session returns existing-style API 401
      and does not execute the handler under the session.
- [ ] Empty Authorization/query/form credential plus valid session returns API
      401 with no fallback.
- [ ] No explicit credential plus valid session succeeds as the session user.
- [ ] No explicit credential plus missing/expired/invalid session returns API
      401, never redirect/HTML.
- [ ] Session user A plus explicit key for user B executes as B, not A.
- [ ] Conflicting explicit values fail rather than selecting either identity.
- [ ] Preserve parameterized plaintext API-key lookup and current token format;
      do not hash, migrate, rotate, or otherwise alter key storage.

### Context, RBAC, and error parity

- [ ] Both successful mechanisms provide equivalent `models.User`, Role, and
      `user_id` request context consumed by handlers.
- [ ] Do not duplicate or bypass `EnforceViewOnly` or `RequirePermission`.
- [ ] Preserve admin, regular-role, view-only, permission-denied, and cross-user
      behavior for API-key and session identities.
- [ ] View-only session users cannot mutate resources they cannot mutate with an
      API key.
- [ ] Preserve existing API auth/permission status codes and JSON response
      conventions as closely as possible: no web redirect or login HTML.
- [ ] API auth errors contain no presented credential or session internals.
- [ ] Preserve OPTIONS/CORS handling for external clients and safe-method
      semantics.
- [ ] Add no auth cache, JWT, second cookie, frontend token, or implicit session.

### Credential-aware same-origin protection

- [ ] Reuse the existing `filippo.io/csrf`/CrossOriginProtection engine and
      trusted-origin configuration. Do not add a token-based CSRF system or
      dependency.
- [ ] Replace the blanket `/api` exemption with a credential-aware decision
      based on the same extractor used by auth.
- [ ] Requests with any explicit API credential present are treated as
      non-ambient API-key attempts for CSRF selection; they keep the historical
      exemption and are subsequently accepted/rejected only by API-key auth.
- [ ] Requests without explicit API credentials use ambient session authority
      and are not exempted from same-origin checks.
- [ ] Session-authenticated POST, PUT, PATCH, DELETE, and any other unsafe API
      methods reject cross-site/same-site/foreign-Origin browser requests.
- [ ] Legitimate same-origin SPA unsafe requests succeed.
- [ ] Session GET/HEAD/OPTIONS remain usable without inventing destructive CSRF
      checks.
- [ ] External API-key clients can continue unsafe requests without a browser
      CSRF token or cookie.
- [ ] Empty/invalid explicit credentials cannot bypass authorization: they may
      skip CSRF as explicit non-ambient attempts but must fail API-key auth.
- [ ] Do not widen CORS or trust new `X-Forwarded-*` values. Preserve existing
      ProxyHeaders/Host/Origin/trusted-origin behavior.
- [ ] Keep deprecated `csrf_token` compatibility fields only where existing
      non-API forms still use them; do not create a fake new token contract.

### Session lifecycle

- [ ] Reuse the existing `gophish` session and current cookie/store/user lookup.
- [ ] Do not change cookie name, keys, HttpOnly, Secure, SameSite, Path, max age,
      login, impersonation, fixation, or restart behavior in this PR.
- [ ] Invalid/expired session API requests fail without panic.
- [ ] After logout, a no-credential API request using the prior cookie fails.
- [ ] No session is created implicitly by an API auth failure.

### Frontend transport decoupling

- [ ] `requestJSON` no longer reads `user.api_key` or emits an Authorization
      header/query/form credential.
- [ ] Preserve explicit `credentials:"same-origin"` and every existing URL,
      method, JSON/body, business header, response parsing, APIRequestError,
      abort, 4xx/5xx, return, and wrapper contract.
- [ ] The direct group CSV upload no longer reads/sends the API key and
      explicitly or naturally uses same-origin session cookies.
- [ ] Search all first-party frontend sources and generated assets; remove every
      API-key consumer used for HTTP transport, not settings management.
- [ ] Campaigns, groups, templates, landing pages, SMTP, IMAP, webhooks,
      settings API actions, imports, and all native wrappers continue to work
      without behavior/race/order changes.
- [ ] `/api/reset` remains callable via session same-origin and continues its
      legacy response/update workflow.

### Global browser exposure and settings exception

- [ ] Remove the `api_key` property entirely from the global `user` object in
      `templates/base.html`; do not set it to an empty/masked value.
- [ ] Standard dashboard/campaign/group/template/page/SMTP/IMAP/webhook pages
      contain no complete API key in HTML source, inline script, DOM, data
      attributes, global/window state, localStorage, or sessionStorage.
- [ ] Standard-page SPA network requests contain no Authorization API key,
      `api_key` query, or `api_key` form field.
- [ ] Use a distinctive synthetic key in browser tests without writing it to
      logs or realistic fixtures.
- [ ] Preserve the dedicated settings input and `settings.js` reset behavior
      temporarily. Settings management exposure remains explicitly documented
      as legacy debt for the verifier/reveal-once PR.
- [ ] Preserve `/api/users` and `/api/reset` response serialization; do not
      change the public API read/reveal contract in this PR.
- [ ] Report honestly:
      global/common-page exposure = none;
      settings/API-management exposure = legacy present.

### Tests

- [ ] Freeze all target auth matrix cases: valid key/no session, valid key+same
      or different session, invalid key+valid session, each empty explicit source
      + session, no explicit+valid session, no explicit+invalid session, and key
      user A/session user B identity.
- [ ] Test Bearer and raw Authorization, query, form, repeated parameters,
      identical duplicates, and distinct conflicts.
- [ ] Prove query/form/body precedence is no longer an accidental source of
      identity when values conflict.
- [ ] Add session-auth API GET, POST, PUT, DELETE, permission denied, view-only,
      cross-user, logout, and expired/invalid-session tests using local fixtures.
- [ ] Test session unsafe same-origin success and cross-site/same-site/foreign
      Origin failure.
- [ ] Test explicit API-key unsafe requests preserve legacy no-CSRF behavior.
- [ ] Test auth failures retain API status/JSON and never redirect/render HTML.
- [ ] Keep existing API-key backend tests green as external-client compatibility
      proof.
- [ ] Browser tests inspect dashboard and representative campaigns/groups/
      templates/pages/SMTP/IMAP/webhooks/settings traffic, proving standard
      pages work and send no API key.
- [ ] Browser tests prove the synthetic key is absent from standard HTML/DOM/
      global state/storage while documenting the dedicated settings exception.
- [ ] Re-run campaign copy/order and all migrated requestJSON wrapper contracts.
- [ ] Re-run encrypted IMAP/SMTP/webhook API/browser/delivery behavior through
      both relevant auth mechanisms.

### Docker and end-to-end

- [ ] Real image test covers login/session cookie, SPA navigation, session API
      GET, same-origin unsafe mutation, cross-origin rejection, invalid/empty
      explicit credential with valid session rejection, valid explicit key
      success, distinct credential conflict rejection, logout then session API
      rejection, and no API-key network/header exposure.
- [ ] No external database, SMTP server, webhook, email, or Internet target is
      contacted by tests.
- [ ] PR #59 bootstrap, PR #60 config-no-log, PR #61 logger-mode, and credential
      keyring container regressions remain green.

### Assets, dependencies, and scope

- [ ] Regenerate only canonical affected frontend assets through the pinned
      pipeline.
- [ ] Two clean frontend builds are byte-identical.
- [ ] `go.mod`, `go.sum`, `package.json`, and `yarn.lock` remain unchanged.
- [ ] Add no dependency.
- [ ] Do not modify API-key schema/storage/format/generation/reset semantics,
      add verifier/pepper/keyring, implement reveal-once, remove query/form
      transports, or add multi-key/scopes/labels.
- [ ] Do not modify IMAP/SMTP/webhook encryption, keyring, bootstrap, logger,
      Docker configuration logging, TLS, `events.details`, or business
      permissions.
- [ ] Do not claim API-key browser exposure is entirely eliminated while
      settings and user-management APIs still expose it.

### Documentation and security evidence

- [ ] Document the architecture:
      SPA uses same-origin web session;
      external clients continue API key;
      any explicit API credential selects API-key auth and invalid never falls
      back;
      distinct explicit credentials are rejected;
      unsafe session mutations use existing same-origin checks;
      safe methods and API-key clients remain compatible.
- [ ] Document retained raw/query/form transports as legacy, with query logging
      risk deferred.
- [ ] Document plaintext storage, settings/API exposure, verifier/pepper, and
      reveal-once as intentionally deferred to the next PR.
- [ ] Formal self-review maps every matrix, RBAC, CSRF, frontend, Docker,
      regression, dependency, and scanner criterion to evidence.
- [ ] Never log a presented API key, session ID, cookie, synthetic test key, or
      Authorization header.

### Validation

- [ ] `./scripts/verify.sh`, `go test ./...`, `go test -race ./...`,
      `go vet ./...`, and `go build ./...` pass.
- [ ] Full browser suite, real Docker auth contract, credential regressions, and
      frontend double-build reproducibility pass.
- [ ] Run `govulncheck`, `gosec`, Gitleaks, actionlint, zizmor, Yarn audit,
      Retire.js, and dependency-delta review. Report unavailable/non-wired tools
      honestly; add no suppression.
- [ ] Final searches find no frontend transport use of `user.api_key`,
      Authorization API-key header, query/form API key, or global base key
      injection outside explicitly retained management/tests/docs.
- [ ] `git diff --check`, scope/dependency diff, formal self-review, and clean
      status pass before push.

### Git and delivery

- [ ] Every branch commit has Author and Committer exactly
      `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`.
- [ ] Every commit is signed and locally reports GOOD.
- [ ] No author, committer, co-author trailer, message, or metadata refers to
      Copilot. The explicit user instruction overrides the default trailer.
- [ ] Signed Conventional Commits use why-focused bodies and Goal role markers;
      `Assisted-by: Claude:*` is allowed.
- [ ] Push only after full local gates.
- [ ] PR description includes every auth selection, CSRF, compatibility,
      frontend/settings exception, Docker, regression, dependency, test, and
      scanner field required by the user specification.
- [ ] Explicitly state that API-key storage remains plaintext and
      reveal-once/verifier is deferred.
- [ ] Merge only after Goal Inspector PASS, independent security/code review
      PASS, all CI green, every GitHub commit Verified under `vg-kvr-grp`, no
      Copilot metadata, no conflict, and no unresolved review conversation.
- [ ] After merge, switch to `main`, fast-forward from `origin/main`, verify a
      clean tree, and stop without starting API-key verifier work.

## Scope Boundaries

**In scope:**
- Dual API authentication selection: explicit API key or existing session.
- Credential-aware existing CSRF/same-origin exemption.
- Equivalent request context/RBAC/error behavior.
- First-party frontend transport migration and removal from global common-page
  state.
- Explicit conflict hardening, tests, assets, Docker, docs, and review evidence.

**Out of scope:**
- API-key hash/verifier/pepper/schema/format/rotation/reveal-once.
- Removal of settings or `/api/users`/`/api/reset` key exposure.
- Removal of raw/query/form API-key transports.
- Cookie policy/login redesign, new CSRF tokens, CORS expansion, auth cache/JWT.
- Credential encryption, bootstrap, logger/Docker hardening, TLS,
  `events.details`, business behavior, and new dependencies.

## Applicable Project Conventions

**Quality gate commands:**
- `./scripts/verify.sh`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build ./...`
- Full browser, Docker, frontend reproducibility, and scanners above.

**Commit convention:**
- Signed Conventional Commits using `git commit -S`, with reason in the body.
- Builder/Inspector `[B]`/`[I]` markers required by Goal.
- `Assisted-by: Claude:Sonnet-4.6` / `Claude:Haiku-4.5` allowed.
- Author/Committer exactly
  `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`.
- No Copilot author, committer, co-author, trailer, message, or metadata.

**Guidelines:**
- `CLAUDE.md`
- `SECURITY.md`
- `CONTRIBUTING.md`
- `.github/PULL_REQUEST_TEMPLATE.md`
- No `AGENTS.md`, `CONSTITUTION.md`, `.agents/guidelines`, or
  `.github/guidelines` exist.

**Rules:**
- Never work/push directly on `main`.
- Never log/store real credentials in tests/docs.
- Explicit invalid credentials never inherit ambient session authority.
- Preserve RBAC and external API-client compatibility.
- No dependency, verifier, schema, or hidden scanner bypass.
- Full local gates, independent reviews, green CI, formal self-review, verified
  identity/signatures, and no open thread before merge.
