# Independent Review Feedback — Iteration 5

## Verdict: FAIL

The security review passed. The guard still misses ordinary prose recommending
deprecated transports without assignment or table syntax, for example:

```text
Use the api_key query parameter for authentication.
```

## Required correction

- Detect common recommendation phrasing around `api_key`: `use`, `using`,
  `via`, `with`, `accepts`, and `authenticate with`, absent same-line
  deprecation/migration context.
- Add positive fixtures for query/form recommendation variants and casing.
- Add negative fixtures for schema/internal references, migration/deprecation
  descriptions, historical statements, and guard meta-documentation.
- Keep matching narrow enough that arbitrary internal `api_key` mentions remain
  allowed.
- Preserve encoded-parameter, Bearer-casing, scanner-error, table and alternative
  wording rules.
- Keep runtime/auth/version/dependencies unchanged and rerun full gates.
