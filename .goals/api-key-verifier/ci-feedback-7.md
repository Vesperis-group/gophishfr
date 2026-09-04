# CI Feedback — Iteration 7

## Verdict: FAIL

All executable CI jobs passed except the container lifecycle. The initial run
was blocked by GitHub billing and contained no steps; after billing was restored,
the rerun executed normally and failed here:

```text
Test API verifier container behavior
Error: stepping, attempt to write a readonly database (8)
```

## Root cause

The bootstrap container creates `/state/gophish.db` as the image's non-root
`app` UID. The harness then stops the container and invokes host `sqlite3` to
recreate a legacy row. GitHub-hosted runner UID differs from the image UID, and
the database is not host-writable. The state directory being `0777` does not
change ownership/write bits of the already-created DB file.

## Required correction

- After bootstrap and before any host-side SQLite write, use a task-owned
  short-lived container as root to make only the synthetic test database
  writable by both runner and app, following the established keyring harness
  ownership/permission pattern.
- Do not broaden keyring permissions or production behavior.
- Verify host sqlite3 mutation and subsequent non-root app container writes both
  succeed.
- Add an explicit ownership/mode assertion so CI UID differences remain covered.
- Preserve cleanup, all no-log assertions, migration/runtime behavior, and
  script mode `100755`.
- Re-run ShellCheck, focused harness locally, full verify, Docker regressions,
  then CI.
