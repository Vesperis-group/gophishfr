# API-Key Session Decoupling Summary

## Outcome

The goal passed independent inspection, security review, and final code review
after two Builder iterations. First-party browser API requests now use the
existing same-origin web session without carrying the long-lived API key, while
all explicit external API-key transports and permissions remain compatible.

## Acceptance Criteria Achieved

- A shared extractor distinguishes absent, empty, invalid, duplicate, and
  conflicting Authorization/query/form credentials.
- Any explicit credential selects API-key-only authentication; invalid or empty
  values never fall back to a valid session.
- Identical duplicates are accepted; distinct explicit values are rejected.
- No explicit credential selects the existing session-only path.
- Both mechanisms populate the same user/role/user_id context and share existing
  view-only/RBAC middleware and API JSON error behavior.
- Unsafe session API requests use the existing Fetch-Metadata/Origin same-origin
  protection; explicit API-key clients retain the legacy CSRF exemption.
- Sessions requiring a password change are denied API access until reset.
- Campaign completion uses POST for the SPA/session path; session GET cannot
  mutate, while legacy explicit API-key GET remains supported.
- `requestJSON` and direct group upload no longer send the API key.
- The global base-page user object no longer contains `api_key`; standard pages,
  DOM/state/storage, and first-party network requests are key-free.
- Dedicated settings, `/api/users`, `/api/reset`, plaintext storage, and legacy
  raw/query/form transports remain intentionally unchanged for the verifier PR.
- Canonical frontend assets rebuild reproducibly.
- Full Go, browser, Docker, credential-regression, dependency, and scanner gates
  pass.

## Iteration History

1. Builder iteration 1 implemented dual authentication, conditional same-origin
   protection, frontend decoupling, tests, Docker, docs, and self-review in
   commit `2097c482d01051674c07fc474ee42a59d4ac61af`.
2. Goal Inspector iteration 1 returned PASS in commit
   `ee6037b3ffc56059122677c9a9dd7b10ad504b8e`.
3. Security/code reviews found that forced password change could be bypassed
   through session API access and campaign completion mutated through GET.
4. Builder iteration 2 blocked unfinished-reset sessions and moved session/SPA
   campaign completion to protected POST in commit
   `bbea0a3ebda86b6a6e1f2803c0ba85abc697bf4b`.
5. Goal Inspector iteration 2 returned PASS in commit
   `05928046f5ebdf5966068d9dde6a58177bcf4823`.
6. Final independent security and code reviews returned PASS.

## Review Resolution

Session API authentication now respects the same mandatory password-rotation
boundary as web navigation. A non-forgeable internal auth-mechanism marker
allows campaign completion to preserve legacy API-key GET compatibility while
requiring session clients to use CSRF/view-only-protected POST.

## Recommendations

- Implement the API-key verifier, pepper lifecycle, plaintext migration, and
  reveal-once UX in the next dedicated PR.
- Retain removal/deprecation of query/form API-key transports as separate future
  hardening.
- Keep `events.details` independently scoped.
