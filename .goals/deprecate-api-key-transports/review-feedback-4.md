# Independent Review Feedback — Iteration 4

## Verdict: FAIL

The security review passed. Code review found four correctness gaps:

1. The gate accepts lowercase `bearer`, while runtime recognizes only exact
   `Bearer `.
2. Percent-encoded parameter names such as `api%5Fkey` decode to the supported
   runtime key but bypass literal matching.
3. Scanner errors, including oversized lines, are ignored and can silently skip
   the rest of a document.
4. Documentation says Content-Type must match exactly, while normal media-type
   parameters such as `charset=UTF-8` are accepted.

## Required correction

- Match header names case-insensitively but require runtime-compatible exact
  Bearer scheme casing; keep unrelated schemes explicitly allowed.
- Decode/parse candidate query/form parameter names and detect encoded api_key.
- Propagate scanner errors and make the CLI fail closed; test an oversized line
  followed by a forbidden example.
- Correct Content-Type wording to parsed media type with parameters permitted.
- Keep runtime/auth/headers/status/version/dependencies unchanged.
