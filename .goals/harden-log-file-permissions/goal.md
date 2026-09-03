# Goal: Harden application log-file permissions

## User Request

On branch `security/harden-log-file-permissions`, make newly created GophishFR
application log files private by default and safely harden existing permissive
regular log files when opened. Preserve append behavior, existing contents,
stderr output, log format, and all application behavior. Reject symlinks and
unexpected filesystem object types without chmodding an unrelated target, add
no dependencies or permission configuration, and keep the change small.

The complete user specification in
`pasted-text-8135596f-a042-4386-81e6-4399518864a6.txt` is authoritative.

## Refined Goal

Change the logger's file creation ceiling from `0644` to `0600`, then use the
opened file descriptor to verify a regular file, verify that the configured path
still names the opened inode, and remove group/other/execute permissions from
existing writable files. Never follow a configured symlink for hardening and
never add permissions to an already more-restrictive file. Fail the configured
file sink rather than continue with an insecure/unexpected object, while leaving
stderr and all log content unchanged.

## Measured Legacy Contracts

- `logger.Setup` receives `config.Logging.Filename` from `logging.filename`.
- Empty filename leaves the default Logrus stderr sink only.
- A configured filename is opened with
  `os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644`.
- After open, `Logger.Out` becomes `io.MultiWriter(os.Stderr, file)`.
- Open failure is returned and startup terminates; there is no fallback from a
  configured broken file sink.
- Existing files are not chmodded. A legacy file created under umask `022` or
  `000` is `0644`; reopening an existing `0644` file keeps `0644`.
- Legacy open follows symlinks. No documentation, test, or supported deployment
  contract relies on a symlinked application log path.
- There is no log rotation. The file remains open for the process lifetime and
  append flags preserve previous content.
- Docker's default `logging.filename` is empty, so normal containers use stderr
  only. Native/Ansible configuration sets `gophish.log` and exercises the
  persistent file path.
- Windows builds/releases are supported. Unix permission-bit guarantees cannot
  be claimed for Windows, but production code must remain portable.
- PR #59 removed the initial administrator password from all sinks; PR #60
  removed the Docker configuration dump. This change must not hide or regress
  either content-level guarantee.

## Acceptance Criteria

### Creation and umask

- [ ] Open a configured log file for write/append/create with creation mode
      `0600`, preserving `O_WRONLY|O_APPEND|O_CREATE` and no-truncate behavior.
- [ ] A new Unix log file has effective mode exactly `0600` under normal umask
      `022` and permissive umask `000`.
- [ ] Deleting the log while the service is stopped and restarting recreates it
      at `0600`.
- [ ] Do not add a configurable permission mode, ownership option, rotation
      system, dependency, or non-portable production syscall.

### Existing-file hardening

- [ ] An existing regular service-writable `0644`, `0666`, `0777`, `0640`, or
      `0604` log is hardened to `0600` before becoming a logger sink.
- [ ] Hardening removes group read/write/execute, other
      read/write/execute, and owner execute permissions.
- [ ] Existing content remains byte-for-byte present and new records append
      after it; no truncate/recreate/rename occurs.
- [ ] An existing mode already at `0600` remains `0600`.
- [ ] Never add permission bits to a more-restrictive existing file. If it cannot
      be opened for append or safely secured by the service identity (for
      example `0400`, foreign-owned, or chmod denied), return a clear error and
      leave its mode/content unchanged.
- [ ] Never call `chown`, invoke privilege escalation, or continue silently with
      a configured file that remains group/world accessible.

### Symlink, identity, and non-regular safety

- [ ] A configured path that is already a symlink is rejected. No supported
      documented compatibility contract requires symlinked log files.
- [ ] Reject a directory, FIFO, device, socket, or other non-regular object with
      a non-sensitive actionable error.
- [ ] Use a portable pre-open `Lstat` check for existing symlink/non-regular
      paths so ordinary FIFOs are rejected without blocking.
- [ ] After open, use descriptor `Stat` to require a regular file, then `Lstat`
      the configured path and `os.SameFile` to verify it still names the opened
      inode. Close and fail on mismatch, replacement, or symlink.
- [ ] Harden through `File.Chmod` on the verified open descriptor, never
      `os.Chmod(path)`, so a later pathname swap cannot redirect chmod.
- [ ] Assign the descriptor to `Logger.Out` only after every type, identity, and
      permission check succeeds.
- [ ] Symlink rejection leaves the target's content and mode unchanged.
- [ ] Errors may include the configured path and failure class but never file
      contents or newly exposed sensitive information.
- [ ] Document the residual local race/threat boundary honestly; do not add
      Unix-only `O_NOFOLLOW` to cross-platform production code unless a proven
      unavoidable defect requires build-tagged implementations.

### Permission semantics and portability

- [ ] Derive an existing file's hardened mode by removing permissions outside
      the owner read/write set, rather than adding missing owner permissions.
- [ ] On Unix, all normal writable permissive modes covered above end at `0600`.
- [ ] On Windows, logger setup continues to build and run with standard-library
      APIs. Documentation clearly scopes exact Unix mode guarantees and does not
      claim Windows ACL protection.
- [ ] Unix-only mode, umask, FIFO, and symlink assertions are build-tagged or
      otherwise isolated so Windows tests/releases compile.
- [ ] Umask testing runs in a subprocess or equivalent isolation; do not mutate
      process-global umask in parallel tests.

### Logger behavior preservation

- [ ] Keep stderr as a simultaneous sink for configured file logging.
- [ ] Preserve Logrus formatter, levels, output content, timestamps, and setup
      ordering apart from the file-safety gate.
- [ ] Preserve append semantics and startup failure behavior for configured
      unopenable paths.
- [ ] Do not add log rotation or change log paths/configuration.
- [ ] Multiple sequential setup/test invocations close task-owned descriptors or
      isolate global Logger state so tests do not leak files or race.

### Security regressions

- [ ] Re-run PR #59 bootstrap sentinels: initial administrator plaintext remains
      absent from logger, stderr, files, Docker logs, configuration, and database
      plaintext.
- [ ] Re-run PR #60 config sentinels: full config, DSN, `db_path`, and synthetic
      credentials remain absent from runtime/container logs.
- [ ] Re-run credential/keyring sentinels: keyring and encrypted
      IMAP/SMTP/webhook secret content remains absent.
- [ ] Tightening file permissions must not be used to mask a content-level leak;
      anti-leak assertions inspect contents/sinks independently.

### Focused tests

- [ ] Add logger file tests for new `0600`, umask `000`, recreation, existing
      `0600`, and hardening from `0644`, `0666`, `0777`, `0640`, and `0604`.
- [ ] Test an existing more-restrictive/non-writable file remains unmodified and
      setup fails where the platform/service identity cannot safely append.
- [ ] Test append/no-truncate with an old marker and new log entry.
- [ ] Test missing parent/invalid path and directory rejection.
- [ ] Test symlink rejection and prove target mode/content remains unchanged.
- [ ] Test FIFO/non-regular rejection without hanging.
- [ ] Test descriptor/path identity mismatch or the reachable replacement guard
      deterministically where practical.
- [ ] Test errors do not include file contents.
- [ ] Verify configured logging still emits the same record to stderr and file.
- [ ] Test native Linux filesystem behavior and, where a configured log file is
      exercised in a container, verify its mode with `stat`. If the default
      container remains stderr-only, document that and do not invent a
      persistent file contract.

### Documentation and scope controls

- [ ] Add concise documentation: GophishFR-created application log files use
      mode `0600` on Unix; writable existing permissive regular files are
      hardened on open; unsafe symlink/non-regular/unsecurable paths fail.
- [ ] State that this protects the persistent application file only, not root,
      host compromise, stderr, journald, or Docker logging backends.
- [ ] This PR changes file permissions/safety only. Do not change any logged
      message, formatter, level, sink content, or stderr behavior.
- [ ] Leave `docker/run.sh`, initial-admin bootstrap, API keys/session auth,
      IMAP/SMTP/webhook encryption, keyring, config schema, frontend, Ansible,
      TLS, and `events.details` unchanged.
- [ ] Add no dependencies; `go.mod`, `go.sum`, `package.json`, and `yarn.lock`
      remain unchanged.
- [ ] No frontend source or generated asset changes.

### Validation and review

- [ ] Run focused logger tests, including isolated Unix umask and non-regular
      cases.
- [ ] `./scripts/verify.sh`, `go test ./...`, `go test -race ./...`,
      `go vet ./...`, and `go build ./...` pass.
- [ ] Windows cross-build/test compilation for affected packages passes.
- [ ] Real native Unix file-mode assertions pass.
- [ ] Relevant Docker, secure-bootstrap, config-no-log, and credential-keyring
      regressions pass.
- [ ] Run `govulncheck`, `gosec`, Gitleaks, actionlint, zizmor, ShellCheck when
      scripts are involved, Yarn audit, and Retire.js as applicable. Report
      unavailable/non-wired tools honestly; add no suppression.
- [ ] New gosec findings equal zero; permission hardening must not be bypassed.
- [ ] Final repository search distinguishes the primary logger from unrelated
      file creation and finds no group/world-readable creation of the main
      application log.
- [ ] `git diff --check`, dependency/scope diff, formal self-review, and clean
      status pass before push.
- [ ] Keep the diff small and reviewable: logger implementation, focused tests,
      concise docs, Goal/review artifacts, and only directly necessary gate
      wiring.

### Git and delivery

- [ ] Every branch commit has Author and Committer exactly
      `vg-kvr-grp <killian.vanruymbeke@vesperisgroup.com>`.
- [ ] Every commit is signed with the configured verified GPG key and locally
      reports GOOD.
- [ ] No author, committer, co-author trailer, message, or metadata refers to
      Copilot. The explicit user instruction overrides the default trailer.
- [ ] Signed Conventional Commits use a why-focused body and Goal role markers
      where applicable; `Assisted-by: Claude:*` is allowed.
- [ ] Push only after full local gates.
- [ ] PR description documents threat, legacy/new modes, existing-file
      hardening, umask, symlink/non-regular/descriptor policy, append/content,
      stderr, Docker/native/Windows behavior, #59/#60 regressions, dependencies,
      tests, gosec, and self-review.
- [ ] Explicitly state: this PR changes file permissions only and does not alter
      log content or stderr behavior.
- [ ] Merge only after Goal Inspector PASS, independent security/code review
      PASS, all CI green, every GitHub commit Verified under `vg-kvr-grp`, no
      Copilot metadata, no conflict, and no unresolved review conversation.
- [ ] After merge, switch to `main`, fast-forward from `origin/main`, verify a
      clean tree, and stop without starting API-key session decoupling.

## Scope Boundaries

**In scope:**
- Primary application log-file creation mode and safe hardening on open.
- Symlink/non-regular/identity validation directly required to avoid unsafe
  chmod.
- Focused Unix/portable tests, concise documentation, and security regressions.

**Out of scope:**
- Log content, stderr, journald, Docker logging driver, rotation, paths, or new
  permission configuration.
- Bootstrap, Docker config dump, API keys/session, credential encryption,
  frontend, Ansible, TLS, and `events.details`.
- Chown/privilege escalation, Unix-only production syscall framework, or new
  dependencies.

## Applicable Project Conventions

**Quality gate commands:**
- `./scripts/verify.sh`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build ./...`
- Focused Unix/logger/container/scanner checks listed above.

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
- Never expose or commit real secrets; synthetic local-only sentinels.
- Fail configured insecure file logging rather than continue silently.
- Preserve content/stderr and all adjacent security contracts.
- No dependency or hidden/bypassed scanner failure.
- Full gates, independent reviews, green CI, formal self-review, verified
  signatures/identity, and no open thread before merge.
