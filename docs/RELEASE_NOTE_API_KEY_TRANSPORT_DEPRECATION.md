# Release note: API-key transport removal (`0.13.0`)

This is a draft release-note entry for the `0.13.0` GophishFR GitHub
Release. `VERSION` is unchanged by this PR; paste this entry into the
manually authored release notes when the containing release is published.

## Removed

- **Query-string `api_key` parameter** was removed in `0.13.0`. A request
  carrying one is rejected with the existing API JSON `401` response and no
  fallback, including an empty value and including a request that also
  carries a valid session or a valid `Authorization` credential.
- **Form-body `api_key` parameter** was removed in `0.13.0`, under the exact
  same rejection rule.

## Deprecated (unchanged by this release)

- **Raw Authorization header, carrying only the token with no `Bearer`
  prefix,** remains documented as deprecated legacy, with no removal version
  announced yet. This release does not change its acceptance.

`Authorization: Bearer <token>` is, and remains, the only recommended and
canonical API-key transport.

## Action required for operators

- Any client still sending `api_key` as a query or form parameter **must**
  migrate to `Authorization: Bearer <token>` before upgrading to `0.13.0`,
  or it will lose API access entirely, not merely receive a deprecation
  warning. See
  [`docs/API_KEY_TRANSPORT_DEPRECATION.md`](../docs/API_KEY_TRANSPORT_DEPRECATION.md)
  for migration examples.
- **Rotate (reset) any API key that was ever sent as a query parameter.**
  Query strings were commonly captured by access logs, reverse-proxy logs,
  browser history, and shell history; migrating the client, or this
  release removing the transport, does not erase a copy already written
  before migration.
- Review and remove historical copies of that key from your own access logs,
  reverse-proxy logs, and any copied diagnostics, under your retention and
  incident-handling policy. GophishFR cannot identify affected clients or
  clean logs on your behalf.

## What changed in `0.13.0`

Any request using the query or form `api_key` transport — including an
empty value, and including one that also carries a valid session or a valid
`Authorization` credential — is now rejected with the existing API JSON
`401` response, with no fallback. Raw `Authorization` was not part of this
removal and stays accepted and deprecated afterward.

## Not in this release

No RFC 9745 `Deprecation` header, RFC 8594 `Sunset` header,
`Link: rel="deprecation"` header, custom header, or server-side warning was
added. See `docs/API_KEY_TRANSPORT_DEPRECATION.md` for why. No dependency
changed, no `VERSION` bump, and no change to raw `Authorization` or session
authentication behaviour.
