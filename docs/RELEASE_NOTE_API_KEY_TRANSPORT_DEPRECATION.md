# Release note: API-key transport deprecation

This is a draft release-note entry for the next GophishFR GitHub Release.
`VERSION` is unchanged by this PR; paste this entry into the manually
authored release notes when the containing release is published.

## Deprecated

- **Query-string `api_key` parameter** is now documented as deprecated.
  Removal is targeted for `0.13.0`.
- **Form-body `api_key` parameter** is now documented as deprecated.
  Removal is targeted for `0.13.0`.
- **Raw Authorization header, carrying only the token with no `Bearer`
  prefix,** is now documented as deprecated legacy, with no removal version
  announced yet.

`Authorization: Bearer <token>` is, and remains, the only recommended and
canonical API-key transport. No transport's actual authentication behaviour
changed in this release: all four continue to work exactly as before.

## Action required for operators

- Migrate any client still using a query parameter, form parameter, or raw
  `Authorization` header to `Authorization: Bearer <token>`. See
  [`docs/API_KEY_TRANSPORT_DEPRECATION.md`](../docs/API_KEY_TRANSPORT_DEPRECATION.md)
  for before/after examples.
- **Rotate (reset) any API key that was ever sent as a query parameter.**
  Query strings are commonly captured by access logs, reverse-proxy logs,
  browser history, and shell history; migrating the client does not erase a
  copy already written before migration.
- Review and remove historical copies of that key from your own access logs,
  reverse-proxy logs, and any copied diagnostics, under your retention and
  incident-handling policy. GophishFR cannot identify affected clients or
  clean logs on your behalf.

## What changes in `0.13.0`

`0.13.0` is expected to remove the query and form `api_key` transports. Any
request still using either — including an empty value, and including one
that also carries a valid session or a valid `Authorization` credential —
will be rejected with the existing API JSON `401` response, with no
fallback. Raw `Authorization` is not part of that removal and stays
accepted and deprecated afterward.

## Not in this release

No RFC 9745 `Deprecation` header, RFC 8594 `Sunset` header,
`Link: rel="deprecation"` header, custom header, or server-side warning was
added. See `docs/API_KEY_TRANSPORT_DEPRECATION.md` for why. No dependency
changed, no `VERSION` bump, and no runtime authentication behaviour changed.
