# Independent Review Feedback — Iteration 1

## Verdict: FAIL

### Finding 1 — Form migration example is not executable

The documented old/new form examples use campaign creation with an
`application/x-www-form-urlencoded` business body and omit the route's trailing
slash. The actual handler requires JSON, so both examples fail independently of
authentication and cannot prove a successful migration.

**Required correction:**

- Replace the example with a real executable API request whose business contract
  remains valid while form `api_key` is extracted, or clearly separate the
  authentication-form demonstration from business form semantics.
- Include the exact valid route/method/body.
- Add an executable regression against the real router proving old and new
  examples authenticate and perform the same operation during deprecation.

### Finding 2 — Documentation gate is incomplete

The grep gate misses common form syntaxes such as standalone `api_key=TOKEN`,
`curl -F`, and `--data-urlencode`, and treats header names case-sensitively. It
can also flag unrelated `Authorization: Basic` examples.

**Required correction:**

- Add fixture-based positive/negative tests for every supported deprecated
  syntax and legitimate non-API-key Authorization schemes.
- Match Authorization header names case-insensitively.
- Detect form `api_key` across common curl/form encodings without parsing all
  Markdown.
- Distinguish raw API-key examples from Bearer and unrelated schemes.
- Keep explicit exemptions narrow and auditable.

### Finding 3 — Gate is not enforced by GitHub CI

`scripts/verify.sh` invokes the new gate, but the CI workflow runs individual
gates and never invokes this script. A PR can reintroduce deprecated canonical
examples while required checks remain green.

**Required correction:**

- Add a blocking CI step/job invoking the docs gate.
- Preserve pinned actions, minimal permissions, timeout, and workflow security.
- Keep local `verify.sh` integration.

All fixes must remain documentation/test/CI only. Runtime auth, status codes,
headers, warnings, middleware, version, dependencies, and compatibility behavior
must remain unchanged.
