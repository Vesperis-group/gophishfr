# Security Review Feedback — Iteration 1

## Verdict: FAIL

Independent security and code reviews found two high-confidence authorization
issues directly introduced/exposed by session API authentication.

## Finding 1 — Forced password change bypass

**Severity:** High
**Affected path:** `middleware/middleware.go`

Login establishes the web session before `RequireLogin` redirects an initial
administrator to `/reset_password`. The new session API branch accepts that
session without checking `PasswordChangeRequired`, allowing the temporary
bootstrap credential to mutate `/api/*` resources before completing the
mandatory rotation.

**Required correction:**

- Reject session-authenticated API requests while
  `PasswordChangeRequired == true`.
- Return a non-sensitive API JSON authorization error, never a redirect or HTML.
- Keep explicit API-key behavior unchanged for compatibility; the new guard is
  specific to ambient session authentication.
- Add tests for initial login/session, API GET and mutation rejection, password
  reset completion, then API success.

## Finding 2 — Mutating campaign completion GET bypasses CSRF/view-only

**Severity:** Medium
**Affected paths:** API campaign completion handler/routes and frontend caller.

Campaign completion is a state-changing GET. Safe methods bypass the existing
same-origin protection and `EnforceViewOnly`, so the new session path permits
cross-site campaign termination and mutation by view-only session users.

**Required correction:**

- Add/use an unsafe POST method for first-party session campaign completion so
  existing same-origin CSRF and `EnforceViewOnly` protections apply.
- Migrate the SPA caller to POST without changing the campaign completion
  business result.
- Preserve legacy explicit API-key GET compatibility if required by the public
  API, but reject session-authenticated GET completion deterministically.
- Carry an internal non-sensitive auth-mechanism marker where necessary; never
  infer from the presence of session plus key after authentication.
- Add tests proving:
  session POST same-origin succeeds;
  session POST cross-origin fails;
  view-only session POST fails;
  session GET fails without mutation;
  cross-site session GET fails/no mutation;
  explicit API-key legacy GET still succeeds;
  invalid/empty explicit credentials still never fall back.
- Search for other mutating GET API endpoints exposed by session auth and either
  prove them read-only or apply the same narrow protection. Do not broadly
  redesign REST routes.

Both fixes must preserve the complete auth-selection matrix, API JSON/no-
redirect semantics, external client compatibility, frontend no-key contract,
RBAC, dependencies, and all original scope boundaries.
