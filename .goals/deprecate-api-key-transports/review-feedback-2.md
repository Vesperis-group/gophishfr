# Independent Review Feedback — Iteration 2

## Verdict: FAIL

`docs/GROUP_IMPORT_LIMITS.md` still lists an `api_key` parameter as a normal
authentication option without deprecation context. This contradicts the new
contract that Bearer is canonical and query/form are deprecated.

The documentation guard reports the file clean because it recognizes assignment
and curl syntax, but not documentation tables that advertise an `api_key`
parameter as an ordinary supported option.

## Required correction

- Update the group import authentication table to recommend Bearer.
- Mark parameter-based `api_key` authentication deprecated and targeted for
  removal in `0.13.0`, linking to the migration guide.
- Extend docsguard or add a focused fixture/assertion detecting canonical docs
  that present `api_key` parameters as normal without deprecation context.
- Add positive/negative fixtures so legitimate deprecation-policy references
  remain allowed.
- Search all shipped Markdown for equivalent table/prose wording.
- Keep runtime/auth/version/dependencies unchanged and rerun docs/CI/full gates.
