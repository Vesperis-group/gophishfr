# Formal self-review: API key/session decoupling

## Architecture and threat contract

- Authentication mechanism selection is centralized in
  `extractExplicitAPICredential`. Presence is independent of value, all repeated
  Authorization/query/form values are collected, identical duplicates collapse,
  and distinct values fail without order-dependent precedence.
- `RequireAPIKey` validates explicit credentials only when present and otherwise
  consumes the existing session user from `GetContext`. It never attempts one
  mechanism after the other fails.
- Both paths set the same user and `user_id` context before the unchanged
  `EnforceViewOnly` and `RequirePermission` middleware.
- `CSRFExceptions` uses the same extractor. Only explicit non-ambient API-key
  attempts skip the existing CrossOriginProtection engine; session-authorized
  unsafe methods remain protected.

## Contract evidence

| Area | Evidence |
|---|---|
| Explicit source matrix and duplicates | `middleware/api_auth_test.go` covers Bearer, raw Authorization, query, form, repeated identical values, cross-source identical values, and same/cross-source conflicts. |
| Mechanism selection and identities | `TestAPIAuthenticationMechanismSelection` covers key only, key plus another session identity, invalid/empty explicit values plus session, session only, missing/invalid session, and conflict plus session. |
| JSON/no redirects/CORS | Middleware selection assertions require JSON `401` with no `Location`; existing CORS/OPTIONS tests remain unchanged. |
| RBAC and view-only parity | `TestSessionAndAPIKeyRBACParity` runs admin, view-only mutation, and system-permission cases through both mechanisms and the existing middleware. |
| Real session lifecycle and methods | `TestSessionAPIAuthenticationContract` logs in through the real admin chain and exercises session GET/POST/PUT/DELETE, logout, API-key-after-logout, empty/invalid/conflicting credentials, and JSON errors. |
| CSRF/same-origin | Middleware and controller contracts cover same-origin success, Fetch-Metadata cross-site/same-site rejection, foreign-Origin rejection, safe methods, and explicit-key unsafe compatibility. |
| Frontend transport | `requestJSON` and group CSV upload use same-origin cookies without Authorization; browser wrapper tests assert no Bearer transport while preserving payload, error, and ordering contracts. |
| Browser exposure | `api-key-decoupling.spec.ts` checks representative standard pages, DOM/global state, storage, cookies, API headers/query/form traffic, and the dedicated settings exception. |
| Container contract | `scripts/test-container-api-session-auth.sh` covers real-image login, password lifecycle, SPA pages, session read/mutation, cross-site rejection, explicit invalid/empty/conflicting values, API-key compatibility, settings exception, logout, and log non-exposure. |
| Encrypted credentials and business wrappers | Existing IMAP, SMTP, webhook, campaign, group, template, landing-page, and wrapper suites remain part of the full Go/browser/container gates. |
| Dependencies and scope | No dependency or schema files are changed. Plaintext storage, reset, API serialization, transports, encryption, keyring, bootstrap, logger, TLS, and business permissions are unchanged. |

## Review resolution

- A pre-commit independent code review identified that malformed URL encoding
  could be dropped by `ParseForm` and then inherit session authority. Parse
  failures are now carried into the shared extractor, select the explicit
  credential path, and receive a JSON 401. Unit and real admin-chain contracts
  cover malformed query and form values with a valid session.
- The same review identified the default plain-text CSRF failure body. The
  existing protection engine now uses a route-aware failure handler: API
  failures are JSON 403 responses with no redirect, while page behavior retains
  the existing diagnostic response.
- The reviewer also noted the pre-existing state-changing campaign-completion
  GET. This iteration intentionally does not change HTTP method or business
  route contracts: the goal requires session GET/HEAD/OPTIONS compatibility,
  preserving every existing frontend method, and no adjacent security change.
  Migrating that legacy endpoint requires a separate compatibility change.
- The complete diff was independently re-reviewed after the malformed-input and
  JSON-CSRF fixes and received a PASS with no remaining high-confidence defect.

## Exposure statement

- Global/common-page API-key exposure: **none**.
- First-party HTTP transport API-key use: **none**.
- Dedicated settings and `/api/users`/`/api/reset` management exposure:
  **legacy present**.
- Plaintext API-key storage and raw/query/form transports: **legacy present**.
- Verifier, pepper, and reveal-once migration: **deferred**.

## Validation record

- Core gates passed: `./scripts/verify.sh`, `go test ./...`,
  `go test -race ./...`, `go vet ./...`, and `go build ./...`.
- Frontend gates passed: two clean `yarn build` runs produced byte-identical
  tracked assets, and `yarn test:browser` passed.
- Production-container contracts passed:
  `scripts/test-container-api-session-auth.sh`,
  `scripts/test-docker-config-no-log.sh`,
  `scripts/test-container-imap-keyring.sh`, and
  `scripts/test-secure-admin-bootstrap-container.sh`.
- `govulncheck ./...` reported zero reachable vulnerabilities (and three
  vulnerabilities in required modules that the program does not call).
- Gitleaks and actionlint passed. Zizmor reported no findings and two existing
  suppressions; this change adds no suppression.
- `yarn audit --groups dependencies` reported zero vulnerabilities. The
  standalone Retire.js executable was unavailable, so Retire.js 5.7.0 was run
  through `npx --yes` and reported no finding.
- `gosec ./...` reported twelve existing findings in unchanged code and no
  finding in changed logic; no suppression or adjacent fix was added.
- `go.mod`, `go.sum`, `package.json`, and `yarn.lock` have no delta from the
  recorded initial SHA. No dependency was added or updated.
- Final frontend searches found no `user.api_key`, Authorization API-key
  transport, query/form API-key transport, or global template injection.
- `git diff --check` passed. Docker contracts removed their own resources;
  unrelated pre-existing stopped containers were left untouched.
