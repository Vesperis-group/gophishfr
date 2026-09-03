# Docker configuration no-log self-review

## Exposure and removal

- Legacy behavior: `docker/run.sh` printed `Runtime configuration:` followed by
  `cat config.json`. A synthetic credential-bearing DSN was present on stdout
  and therefore in Docker logs; it was not present on stderr.
- New behavior: the entrypoint prints only the static `Starting GophishFR`
  boundary message before preserving the existing
  `exec ./gophishfr "$@"` behavior. It never prints configuration contents,
  `db_path`, environment values, or keyring data.
- Sink review: the removed dump was reachable through container stdout,
  attached output, Docker logs, and CI when containers ran there. No separate
  stderr, Ansible, or documentation dump was found.
- Shell review: the entrypoint contains no xtrace, environment dump, secret
  echo, error trap, or keyring-content read. Its `jq` temporary-file mutations
  and replacement behavior are unchanged.

## Regression evidence

`scripts/test-docker-config-no-log.sh` runs the production entrypoint in real
containers and checks attached stdout, attached stderr, separated Docker log
streams, and combined Docker logs. Runtime-generated sentinels cover:

- MySQL- and PostgreSQL-shaped credential-bearing connection strings, passed
  unchanged to a local stub at the application boundary without contacting a
  database;
- a normal SQLite startup using the real application, including database
  creation and the existing `Starting admin server` diagnostic;
- another environment-to-config mutation and administrator bootstrap value;
- malformed JSON, proving parser errors do not reflect the file, recognizable
  JSON keys, or synthetic values.

The harness rejects the legacy heading, configuration keys, and every generated
sentinel, and removes all task-owned containers and temporary files on exit.
The production image, focused lifecycle, secure administrator bootstrap
container suite, and credential-keyring container suite all passed.

## Validation

- `shellcheck docker/run.sh scripts/test-docker-config-no-log.sh`: pass.
- `./scripts/verify.sh`: pass after adding the installed Go and scanner
  directories to this non-interactive shell's `PATH`; this includes gofmt,
  golangci-lint, module verification, vet, build, unit tests, race tests,
  Ansible structure, frontend reproducibility, action pins, and govulncheck.
- `govulncheck ./...` through the pinned `go1.25.13` toolchain: zero reachable
  vulnerabilities.
- `gosec ./...`: 14 pre-existing findings in unchanged Go code (TLS
  compatibility/configuration, XSS/open-redirect reports, operator-selected
  paths, keyring/config paths, and the intentionally unchanged `0644` global
  log mode); no finding originates in this shell/documentation-only product
  change.
- `gitleaks detect --source . --no-banner --redact`: no leaks.
- `actionlint -color`: pass.
- `zizmor --min-severity=low .github/workflows/`: no findings (offline mode,
  two repository suppressions retained).
- `corepack yarn audit`: zero vulnerabilities across 113 packages.
- `npx --yes retire --path static --outputformat text`: pass.

## Scope

Configuration generation, schema, parsing, environment precedence, database
connection strings, signal behavior, administrator bootstrap, API keys,
credential encryption, keyring handling, frontend assets, Ansible, and
dependencies are unchanged. The workflow change only runs the focused container
regression against the image it already builds. `go.mod`, `go.sum`,
`package.json`, and `yarn.lock` are unchanged.

Global application log mode `0644` remains intentionally unchanged and is a
separate hardening backlog item.
