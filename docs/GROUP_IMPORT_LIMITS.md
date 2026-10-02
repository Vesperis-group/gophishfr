# Group import limits

Audit date: 2026-08-20

This document describes the contract enforced by `POST /api/import/group`, the
endpoint the "Bulk Import Users" control calls when a CSV or TXT file is
uploaded on the Users & Groups page.

The endpoint parses the uploaded file and returns the recipients it recognised.
It performs no database write: the returned targets are submitted separately by
the group endpoints, which persist them inside a transaction.

## Accepted contract

| Property | Value |
| --- | --- |
| Method | `POST` only |
| Content-Type | `multipart/form-data` with a boundary |
| Authentication | API key, as the canonical `Authorization: Bearer` header (a deprecated `api_key` query parameter is also still accepted, targeted for removal in `0.13.0`; a `multipart/form-data` field named `api_key` is **not** read as a credential — Go's `ParseForm` never parses a multipart body — so this endpoint is effectively header- or query-authenticated only; see [API-key transport deprecation](API_KEY_TRANSPORT_DEPRECATION.md)) |
| Authorization | `modify_objects`, enforced for every non-`GET` API request |
| File field | `files[]` |
| Files per request | 10 |
| Multipart parts per request | 32, files and other fields combined |
| Total request size | 10 MiB |
| Per-file size | Not enforced separately; the request limit is authoritative |
| Records per request | 50,000 |
| Columns per record | 256 |
| Characters per field | 255 |
| Response | JSON array of targets, or a JSON error object |

A realistic 50,000-recipient export stays close to 6 MiB, so the request limit
leaves a wide margin while keeping every upload bounded. A dedicated per-file
limit would duplicate the request limit without adding protection, because a
request can never exceed 10 MiB regardless of how its files are split.

The limits are constants in `util`. They are technical guard rails rather than
deployment policy, so they are not configuration: adding options would create a
public compatibility surface without making the endpoint safer.

## Rejection behaviour

| Condition | Status | Message |
| --- | --- | --- |
| Method other than `POST` | `400` | `Method not allowed` |
| Content-Type that is not multipart | `415` | `Expected a multipart/form-data upload` |
| Body larger than the request limit | `413` | `The uploaded data is larger than 10485760 bytes` |
| More than 10 files | `400` | `Too many files uploaded at once (maximum 10)` |
| More than 32 parts | `400` | `Too many form parts (maximum 32)` |
| More than 50,000 records | `400` | `Too many records to import at once (maximum 50000)` |
| More than 256 columns in a record | `400` | `Too many columns in a record (maximum 256)` |
| Field longer than 255 characters | `400` | `A field is longer than 255 characters` |
| Invalid CSV syntax | `400` | `The uploaded data is not valid CSV` |
| Any other malformed body | `400` | `Error parsing CSV` |

Messages describe the limit that was exceeded. They never echo uploaded
content, a file name, a file system path, a database detail, or a parser
internal. Oversized values are rejected, never truncated silently.

The field limit applies to the records that become targets. A record skipped
for an unusable address never fails the upload.

## Preserved behaviour

The accepted CSV shape is unchanged:

- headers are matched case-insensitively for first name, last name, email, and
  position, in any order and with `_`, `-`, or space separators;
- a UTF-8 byte order mark, `LF` and `CRLF` line endings, and addresses written
  as `Name <user@example.invalid>` are accepted;
- records with a varying number of fields are accepted, and extra columns are
  ignored;
- a record that stops short of a mapped column reads that column as empty
  rather than inheriting the previous record's value;
- a record whose address cannot be parsed is skipped rather than failing the
  request, even when one of its other fields is too long;
- a file whose header matches none of the four columns contributes no target;
- a body carrying no `files[]` part returns an empty list rather than an error;
- the uploaded file name is metadata only. It is never used to build a path, is
  not trusted as a type control, and the server accepts any extension.

## Field limit and the database

The `targets` table declares `first_name`, `last_name`, `email`, and `position`
as `varchar(255)` in the shipped SQLite and MySQL schemas. The import limit is
255 characters so an accepted import always fits those columns on every engine,
including the ones that enforce the declared width. A test compares the limit
with both schema files and fails if they ever diverge.

Character counting uses runes, so accented or non-Latin names are measured the
way the database column measures them.

## Atomicity

The import endpoint is stateless, so a rejected import leaves nothing behind.

Persisting a group remains `ATOMIC`: `models.PostGroup` opens a transaction and
rolls it back if the group row or any target insertion fails. This change does
not alter that behaviour.

## Memory and processing

The request body is bounded by `http.MaxBytesReader` before it is parsed, and
the multipart stream is read part by part, so an oversized upload is rejected
while it streams instead of being buffered. Records are read one at a time from
`encoding/csv`; the only growing allocation is the list of accepted targets,
which the record limit bounds.

The admin server already applies a 10 second `ReadTimeout`, so no additional
per-request timeout is introduced here. The request, part, file, record,
column, and field limits bound the work a single request can cause.

Multipart parsing never falls back to temporary files: the endpoint uses
`Request.MultipartReader`, which streams, rather than `ParseMultipartForm`,
which spills to disk once its memory budget is exceeded.

## Before and after

| Control | Before | After |
| --- | --- | --- |
| Method enforcement | None, every method reached the parser | `POST` only |
| Content-Type enforcement | None, decided by the multipart reader | Explicit `multipart/form-data` check |
| Request size limit | None | 10 MiB |
| File count limit | None | 10 |
| Per-file size limit | None | Covered by the request limit |
| Record count limit | None | 50,000 |
| Column limit | None | 256 |
| Field length limit | None, a 5 MB field was accepted | 255 characters |
| Multipart part limit | None | 32 |
| Truncated body | Unbounded loop that never returned | Rejected |
| Corrupt multipart boundary | Nil pointer dereference | Rejected |
| Invalid CSV syntax | Silently produced empty targets | Rejected |
| Error status | `500` for every failure | `400`, `413`, or `415` |

No limit existed before this change. The multipart and CSV libraries impose no
size, record, or field bound of their own on a streamed request, so the earlier
behaviour was genuinely unbounded rather than implicitly limited.

## Record isolation

Every target is built from the header and its own record only. A column a
record does not reach reads as an empty value, exactly like a column the record
carries empty, so a shorter record never inherits a value from the record
before it.

Because the address follows the same rule, a record that stops short of the
email column is skipped like any record whose address cannot be parsed, instead
of being imported under the previous record's address.

| Record | Result |
| --- | --- |
| `alice@example.invalid,Alice,Martin,Engineer` | imported with all four values |
| `bob@example.invalid,Bob` | imported as Bob with an empty last name and position |
| `Bob,Roe` under an `Email` header | skipped: no address in this record |
| `bob@example.invalid,Bob,,` | imported, identical to the shorter form above |
