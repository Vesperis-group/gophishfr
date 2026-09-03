# Goal: Stop logging Docker configuration contents

## User Request

On branch `security/stop-docker-config-log`, remove every runtime emission of
`config.json` contents from the Docker entrypoint while retaining only useful,
strictly non-sensitive startup diagnostics. Prove with synthetic sentinels and a
real container that database DSNs and other configuration values no longer
reach stdout, stderr, Docker logs, CI output, or error paths. Keep the change
small and do not introduce redaction machinery, schema changes, dependencies,
or adjacent security work.

The complete user specification in
`pasted-text-ea822e33-cd81-4225-9cce-dc9adf651b2e.txt` is authoritative.

## Refined Goal

Delete the Docker entrypoint's full configuration dump rather than attempting
to parse or redact it. Preserve configuration generation, environment-driven
mutation, parsing, SQLite/MySQL/PostgreSQL connection semantics, keyring checks,
and application startup. Add a focused executable regression proving that a
synthetic credential-bearing `db_path` value and JSON body never appear in any
container output while normal non-sensitive startup diagnostics remain.

## Measured Legacy Contracts

- `Dockerfile` copies the checked-in `config.json`, adjusts the default listen
  address, and runs `CMD ["./docker/run.sh"]`.
- `docker/run.sh` uses `jq` plus a temporary file to mutate configuration from
  environment variables; those intermediate redirects do not emit content.
- `DB_FILE_PATH` replaces `.db_path`, and `DB_NAME` replaces `.db_name`.
- `config.LoadConfig` reads and unmarshals the resulting file.
- `models.Setup` passes `conf.DBPath` directly to `openDatabase`.
- SQLite treats `db_path` as a file path. MySQL passes it directly to
  `gorm.Open("mysql", connectionString)`. PostgreSQL parses it as a PostgreSQL
  connection string. Therefore `db_path` can contain database usernames and
  passwords.
- At `docker/run.sh`'s final startup boundary, the legacy script prints
  `Runtime configuration:` and then executes `cat config.json`.
- The full JSON and a synthetic credential-bearing DSN sentinel were proven
  present on stdout using the real legacy script path. Stderr did not contain
  the sentinel. Container stdout is captured by Docker logs.
- No test or documentation functionally depends on the
  `Runtime configuration:` output.
- The application already emits normal server-start diagnostics after exec.
- `docker/run.sh` contains no `set -x`/xtrace and no `env`, `printenv`,
  `export -p`, or equivalent environment dump.
- The keyring check reports only a non-sensitive variable name/readability
  failure, never keyring content.
- Configuration parse errors report error context/offset, not the complete file.
- PR #59 bootstrap-password anti-leak behavior is independent and must remain.

## Acceptance Criteria

### Surgical removal

- [ ] Remove the `cat config.json` full-content output from `docker/run.sh`.
- [ ] Remove the `Runtime configuration:` heading if it no longer describes a
      valid diagnostic.
- [ ] Do not replace the dump with JSON parsing, allowlists, denylists, masking,
      `jq del`, regex redaction, or field-by-field logging.
- [ ] Configuration contents and values are never printed by the entrypoint.
- [ ] If a replacement diagnostic is retained, it is static/non-sensitive and
      may identify only the constant configuration filename and startup action;
      it must not include `db_path`, server values, environment values, keyring
      paths/content, or any configuration-derived value.
- [ ] Preserve `exec ./gophishfr "$@"` and process/signal behavior.

### Configuration behavior

- [ ] Preserve every existing environment-to-config mutation and its precedence.
- [ ] Preserve the `config.json.tmp` write/replace behavior and error handling;
      no temporary content reaches stdout/stderr.
- [ ] Preserve config schema, field names, default values, parser, CLI
      `--config`, and `db_path` contract exactly.
- [ ] SQLite file-path startup remains functional.
- [ ] MySQL and PostgreSQL continue to receive the same unmodified `db_path`
      connection string. Do not move, parse, sanitize, or redesign database
      credentials.
- [ ] Normal application startup diagnostics remain sufficient to confirm that
      GophishFR started; no configuration value is required for this purpose.
- [ ] A reasonable malformed-config path does not echo the file or synthetic
      sentinel through shell/application errors.

### Zero-output security contract

- [ ] A distinctive synthetic database username/password/DSN sentinel injected
      through the real `DB_FILE_PATH` mutation appears zero times in captured
      stdout, stderr, and `docker logs`.
- [ ] The full JSON body and recognizable keys such as `"db_path"` do not appear
      in entrypoint/runtime logs.
- [ ] No configuration-derived value is emitted through `echo`, `printf`, shell
      traps, failed command rendering, or debug output.
- [ ] `set -x`, `set -o xtrace`, environment dumps, and command tracing remain
      absent.
- [ ] Keyring content is never read for display or emitted. Existing readability
      validation and encrypted IMAP/SMTP/webhook behavior remain unchanged.
- [ ] Initial administrator password from PR #59 remains absent from logger,
      stderr, Docker logs, config, and image/filesystem output.
- [ ] No synthetic test sentinel is hardcoded into production source, docs,
      error messages, or committed fixtures in a way that defeats the test.

### Focused executable regression

- [ ] Add or minimally extend a repository-conventional shell/container test
      that executes the real entrypoint/config mutation path with synthetic
      values and automatically cleans task-owned containers, images, volumes,
      files, and temporary directories.
- [ ] The test captures both stdout and stderr and inspects real Docker logs.
- [ ] The test fails if the sentinel, full config JSON, `db_path` key, or legacy
      `Runtime configuration:` heading appears.
- [ ] The test confirms a strictly non-sensitive startup diagnostic and/or the
      existing `Starting admin server` application log remains.
- [ ] The test proves configuration mutation and load still work and the
      application reaches startup with a normal local SQLite configuration.
- [ ] A separate faithful local-only/stub path may prove that a MySQL/PostgreSQL
      DSN-shaped value is not printed, but it must not contact an external
      database.
- [ ] Exercise a malformed config/error path and prove no full file or sentinel
      reflection.
- [ ] Re-run the existing secure-admin-bootstrap and credential-keyring container
      suites so their zero-secret guarantees remain green.
- [ ] Tests never contact an external database, SMTP server, webhook endpoint,
      or other network service.

### Documentation and scope controls

- [ ] Update only directly relevant Docker/runtime documentation if needed to
      state that configuration contents are intentionally not printed because
      they may contain credentials.
- [ ] Do not change Ansible unless a direct dependency on the Docker config dump
      is proven. PR #59 already removed log scraping.
- [ ] Do not modify global logger code or mode `0644`; retain it as the next
      separate backlog.
- [ ] Do not modify initial-admin bootstrap behavior, API keys/session auth,
      IMAP/SMTP/webhook encryption, keyring format, config schema, database
      formats, TLS, frontend, or `events.details`.
- [ ] Do not add a secret manager, environment variable, parser, redactor,
      dependency, or generic logging framework.
- [ ] `go.mod`, `go.sum`, `package.json`, and `yarn.lock` remain unchanged.
- [ ] No frontend source or generated asset changes.
- [ ] Workflow files remain unchanged unless a direct test-wiring need is
      demonstrated; preserve action SHA pins, permissions, timeouts, and all
      protections if changed.

### Validation and review

- [ ] `shellcheck docker/run.sh` and every changed/new shell test pass without
      suppression or warning.
- [ ] `./scripts/verify.sh`, `go test ./...`, `go test -race ./...`,
      `go vet ./...`, and `go build ./...` pass.
- [ ] Real Docker build, focused config-no-log lifecycle, PR #59 secure-bootstrap
      lifecycle, and credential-keyring lifecycle pass.
- [ ] Relevant SQLite startup and local MySQL/PostgreSQL connection-string
      handling tests pass without external service access.
- [ ] Run `govulncheck`, `gosec`, Gitleaks, actionlint, zizmor, Yarn audit, and
      Retire.js as applicable. Classify existing findings honestly and add no
      broad suppression.
- [ ] Final repository searches find zero runtime pattern that prints config
      contents: `cat.*config`, `config.*cat`, `jq.*config` to stdout, `printenv`,
      `env`, `set -x`, or xtrace.
- [ ] `git diff --check`, dependency/scope diff, formal self-review, and clean
      status pass before push.
- [ ] Add/update concise self-review evidence covering legacy proof, removed
      output, retained diagnostics, sinks, DSN, error path, Docker, #59/keyring
      regressions, dependencies, scanners, and scope.

### Git and delivery

- [ ] Every branch commit has Author and Committer exactly
      `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`.
- [ ] Every commit is signed with the configured verified GPG key and
      `git log --show-signature` reports GOOD.
- [ ] No commit author, committer, co-author trailer, message, or metadata refers
      to Copilot. The explicit user instruction overrides the default trailer.
- [ ] Use signed Conventional Commits with a why-focused body and Goal role
      marker where applicable.
- [ ] Push only after full local gates.
- [ ] PR description documents threat, exact legacy leak and sentinel proof,
      sensitive config fields/DSN, removed output, retained diagnostics, sinks,
      xtrace/env audit, parser/error behavior, SQLite/MySQL, bootstrap/keyring
      regressions, intentionally unchanged logger mode, dependencies, tests,
      ShellCheck, scanners, and formal self-review.
- [ ] Merge only after Goal Inspector PASS, independent security/code review
      PASS, all CI green, every GitHub commit Verified under `vg-kvr-grp`, no
      Copilot metadata, no conflict, and no unresolved conversation.
- [ ] After merge, switch to `main`, fast-forward from `origin/main`, verify a
      clean tree, and stop without beginning logger-mode, API-key, or
      `events.details` work.

## Scope Boundaries

**In scope:**
- Docker entrypoint full-config output removal.
- One static non-sensitive startup diagnostic if useful.
- Focused shell/container anti-leak regression and directly relevant docs/
  self-review.

**Out of scope:**
- Global application logger mode/architecture.
- Initial administrator bootstrap.
- API keys/session/verifier.
- IMAP, SMTP, webhook credentials or keyring format.
- Config schema/DSN redesign, frontend, Ansible without a proven direct
  dependency, TLS, secret managers, or `events.details`.
- General Docker entrypoint refactoring or new dependencies.

## Applicable Project Conventions

**Quality gate commands:**
- `./scripts/verify.sh`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build ./...`
- ShellCheck and repository container harnesses listed above.

**Commit convention:**
- Signed Conventional Commits using `git commit -S`, with reason in the body.
- Builder/Inspector `[B]`/`[I]` markers required by the Goal workflow.
- `Assisted-by: Claude:Sonnet-4.6` for Builder and
  `Assisted-by: Claude:Haiku-4.5` for Inspector.
- Author and Committer must be
  `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`.
- No Copilot author, committer, co-author, trailer, message, or metadata.

**Guidelines:**
- `CLAUDE.md`
- `SECURITY.md`
- `CONTRIBUTING.md`
- `.github/PULL_REQUEST_TEMPLATE.md`
- No `AGENTS.md`, `CONSTITUTION.md`, `.agents/guidelines`, or
  `.github/guidelines` exist.

**Rules:**
- Never commit/push directly to `main`; one security branch and PR.
- Never print or commit real credentials. Synthetic local-only test values only.
- Remove the output rather than redact unknown/future secrets.
- Preserve config parsing, startup, and all adjacent security contracts.
- No new dependency and no hidden/bypassed scanner failure.
- Full local gates, independent reviews, green CI, formal self-review, verified
  signatures/identity, and no open thread before merge.
