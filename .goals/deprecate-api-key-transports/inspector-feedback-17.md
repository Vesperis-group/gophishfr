# Inspector Feedback — Iteration 17

## Verdict: PASS

The user-directed scope reduction has been implemented correctly and completely. The Builder has deleted the entire class of generic Markdown-understanding heuristics (normalize.go, html.go, context.go, credential.go) and replaced them with a fixed, hardcoded registry of exactly the four canonical documents this PR ships, checked via narrow, deterministic literal/regex assertions tied directly to real document content.

---

## Scope Reduction Verification

### 1. Deleted Generic Heuristic Files — CONFIRMED DELETED

- \internal/docsguard/normalize.go\ — **DELETED** (392 lines)
- \internal/docsguard/html.go\ — **DELETED** (412 lines)
- \internal/docsguard/context.go\ — **DELETED** (618 lines)
- \internal/docsguard/credential.go\ — **DELETED** (198 lines)

No re-patching; entire heuristic layer removed.

### 2. Fixed Registry of Exactly Four Canonical Documents — CONFIRMED

\internal/docsguard/docsguard.go\ defines exactly four files:

1. \docs/API_AUTHENTICATION.md\ — Primary contract
2. \docs/GROUP_IMPORT_LIMITS.md\ — Pre-existing table
3. \docs/RELEASE_NOTE_API_KEY_TRANSPORT_DEPRECATION.md\ — Release notes
4. \docs/API_KEY_TRANSPORT_DEPRECATION.md\ — Migration guide

No glob patterns. No file discovery. Static hardcoded slice. Fails closed if document missing.

### 3. Required Invariants Verified in Real Shipped Documents — CONFIRMED

- Bearer canonical recommendation — ✓ Found in docs
- Query parameter deprecated 0.13.0 — ✓ Found in docs
- Form parameter deprecated 0.13.0 — ✓ Found in docs
- Raw Authorization deprecated no removal version — ✓ Found in docs
- Rotation guidance for leaked keys — ✓ Found in docs
- Log cleanup/retention guidance — ✓ Found in docs

Confirmed by: \go run ./cmd/docsguard\ returns 0 violations across all 4 canonical documents.

### 4. Forbidden Literals Correctly Absent — CONFIRMED

\pi_key=\ literal in non-migration canonical docs: 0 occurrences across all three.
\Authorization:\ not followed by Bearer in non-migration docs: 0 violations.

### 5. Migration Guide Before/After Convention — CONFIRMED

- 4 Before (...) headings (deprecated examples allowed)
- 4 After (...) headings (canonical examples required)

Code: \checkCanonicalExamples\ binds to \After (\ pattern, requires literal 
