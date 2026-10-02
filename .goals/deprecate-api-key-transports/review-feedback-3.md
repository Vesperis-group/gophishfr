# Independent Review Feedback — Iteration 3

## Verdict: FAIL

No production `/api` endpoint accepts URL-encoded business form fields. The real
campaign-complete example is executable precisely because its business
semantics live in method/path and its form body contains only the credential.
That proves authentication compatibility, but not the documented migration rule
that an external client must remove only `api_key` and preserve every unrelated
form field.

## Required correction

- Keep the real campaign-complete executable example/test and label it clearly:
  business semantics are method/path; credential-only form body is intentional.
- Add a separate, clearly labelled generic migration template using placeholder
  endpoint/business fields:
  before = `api_key` plus business fields;
  after = same business fields byte-for-byte plus Bearer header, only api_key
  removed.
- State the generic template is illustrative, not a real GophishFR endpoint.
- Add a focused middleware-level test that wraps a local handler with the real
  API-key middleware and proves non-api_key PostForm fields survive parsing/
  authentication unchanged under both deprecated form and canonical Bearer
  variants.
- Clarify Go ParseForm scope: only URL-encoded POST/PUT/PATCH bodies participate;
  multipart api_key is not a supported transport and `/api/import/group`
  remains header-authenticated.
- Correct any statement claiming GET cannot carry a body; the accurate claim is
  that Go does not parse a GET body into PostForm.
- Keep runtime production behavior, auth contracts, version and dependencies
  unchanged.
