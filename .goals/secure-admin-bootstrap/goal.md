# Goal: Secure initial administrator bootstrap

## User Request

On branch `security/stop-temporary-admin-password-log`, permanently remove the
initial administrator plaintext password from every logging sink and replace
the generated/logged bootstrap channel with an explicit operator-provided secret
file, while retaining the existing environment variable as a compatibility
fallback. Fresh installation must fail closed before creating a partial user
when neither source is available, already initialized installations must not
read either source, historical empty-hash installs must recover exactly once,
and the first-login forced password change must remain unchanged.

The complete user specification in
`pasted-text-bb8b10ea-99bf-403b-862d-1b012ea3d863.txt` is authoritative. This
immutable goal incorporates the prior read-only audit and corrected revalidation
of the actual bcrypt and restart behavior.

## Refined Goal

Make the initial administrator password an input secret, never an output:
prefer `GOPHISH_INITIAL_ADMIN_PASSWORD_FILE`, fall back to the existing
`GOPHISH_INITIAL_ADMIN_PASSWORD`, and reject a fresh or historical empty-hash
bootstrap when neither is usable. Resolve, validate, and bcrypt-hash the secret
before a fresh administrator insert, emit no plaintext to logger/stdout/stderr,
and use an empty password hash—not `PasswordChangeRequired`—as the sole marker
for one-time historical recovery. Update Ansible and deployment documentation
to provide the input secret without scraping logs.

## Measured Legacy Contracts

- `models.Setup` creates the first global administrator when `userCount == 0`.
- Initial username is the fixed, non-secret `admin`.
- The administrator row is currently inserted without a password hash.
- `createTemporaryPassword` then runs whenever
  `adminUser.PasswordChangeRequired == true`, even when `Hash` is already set.
- Current password source is `GOPHISH_INITIAL_ADMIN_PASSWORD` when non-empty;
  otherwise `auth.GenerateSecureKey(auth.MinPasswordLength)` generates eight
  random bytes rendered as 16 lowercase hexadecimal characters (64 bits).
- `auth.GenerateSecureKey` uses `crypto/rand` and fails closed on entropy-source
  failure.
- `auth.CheckPasswordPolicy` rejects empty and values shorter than eight bytes;
  it does not define a separate whitespace rule.
- The pinned Go bcrypt implementation rejects input longer than 72 bytes with
  `bcrypt.ErrPasswordTooLong`; it does not silently truncate.
- `createTemporaryPassword` bcrypt-hashes at `DefaultCost`, stores only
  `users.hash`, keeps `PasswordChangeRequired=true`, and logs the plaintext with
  `log.Infof`.
- While `PasswordChangeRequired` remains true, every restart currently
  re-resolves/regenerates, re-hashes, and re-logs the bootstrap password.
- The logger always emits to stderr and additionally to a configured file via
  `io.MultiWriter`. Configured log files are created with mode `0644 & ~umask`;
  existing file permissions are unchanged.
- Container stderr is captured by Docker logging; native service stderr is
  captured by journald. The Ansible role reads and prints `gophish.log` to expose
  the password.
- `PasswordChangeRequired` already redirects an authenticated initial admin to
  `/reset_password`; successful reset stores a new bcrypt hash and clears the
  flag. This behavior is correct and must remain.
- `GOPHISH_INITIAL_ADMIN_PASSWORD_FILE` does not exist.
- `GOPHISH_INITIAL_ADMIN_API_TOKEN` independently seeds the API key and is not a
  password/reset mechanism.
- No offline/local password-reset command or other secure bootstrap channel
  exists.
- `User.Hash == ""` reliably identifies an uninitialized/partial administrator:
  a fresh row has an empty Go string, while bootstrap and password-reset paths
  always store a non-empty bcrypt hash.

## Acceptance Criteria

### Secret source and precedence

- [ ] Add `GOPHISH_INITIAL_ADMIN_PASSWORD_FILE` as the preferred input source.
- [ ] If the FILE variable is set, read only that file. A missing, unreadable,
      empty, invalid, oversized, or otherwise unusable configured file is fatal;
      never silently fall back to the environment value.
- [ ] If FILE is unset and `GOPHISH_INITIAL_ADMIN_PASSWORD` is non-empty, use the
      environment value as the compatibility fallback.
- [ ] If an administrator needs initialization (`userCount == 0` or historical
      `Hash == ""`) and neither source is present, fail closed with a
      non-sensitive actionable error naming the configuration mechanisms.
- [ ] Remove automatic `auth.GenerateSecureKey(8)` password generation entirely
      from bootstrap. Do not add another generated fallback.
- [ ] Do not create any output secret file, print a partial secret, encode it, or
      copy it into configuration.
- [ ] `GOPHISH_INITIAL_ADMIN_API_TOKEN` remains independent and behaviorally
      unchanged.

### Password-file parsing and validation

- [ ] Use a bounded read that rejects excess data before unbounded allocation or
      hashing; never truncate.
- [ ] Accept a normal file or a symlink-backed mounted secret, including the
      Kubernetes-style `..data` pattern. Do not blanket-reject symlinks.
- [ ] Remove at most one final line ending: either `\n` or `\r\n`.
- [ ] Preserve every other byte, including leading, internal, and trailing space
      before the optional line ending. Do not use `strings.TrimSpace`.
- [ ] Reject empty and line-ending-only input.
- [ ] Reject invalid UTF-8 and embedded NUL/binary input.
- [ ] Apply one shared logical validation path to FILE and ENV.
- [ ] Reuse the existing minimum password policy and explicitly enforce the
      pinned bcrypt maximum: accepted logical passwords are at least eight and
      at most 72 bytes. Return a non-sensitive error before bcrypt for
      out-of-range input.
- [ ] Do not invent a separate global password policy or alter ordinary password
      reset/change semantics.
- [ ] Do not chmod, copy, rewrite, delete, or otherwise mutate the operator's
      input file. Documentation recommends restricted `0400`/`0440`-equivalent
      access but implementation remains compatible with real container/K8s
      mounts.
- [ ] Errors may identify the configured path and failure class when useful but
      never include content, buffer, value, length-derived fingerprint, hash, or
      environment value.

### Fresh-install atomicity

- [ ] On `userCount == 0`, resolve and validate the source and compute the bcrypt
      hash before any user row is inserted.
- [ ] Create the initial administrator with username, role, API key, bcrypt hash,
      and `PasswordChangeRequired=true` in one coherent write/transaction
      consistent with existing Setup architecture.
- [ ] Missing/invalid source, file-read failure, policy failure, bcrypt failure,
      API-key generation failure, or DB failure cannot leave a new partial
      administrator row.
- [ ] A failed fresh bootstrap generates no password and emits no secret.
- [ ] Preserve all other historical initial-admin fields and role/API-token
      behavior.

### Historical partial-install recovery

- [ ] An existing initial administrator with `Hash == ""` is the only existing
      user state allowed to read FILE/ENV and initialize a bootstrap password.
- [ ] Recovery resolves, validates, bcrypt-hashes, and stores the password
      exactly once while keeping `PasswordChangeRequired=true`.
- [ ] Recovery failure leaves the existing partial row unmodified and returns a
      clear non-sensitive startup error so a later corrected configuration can
      recover safely.
- [ ] An existing administrator with `Hash != ""` never reads FILE or ENV, never
      recalculates/replaces the hash, and starts normally even when
      `PasswordChangeRequired=true`.
- [ ] After successful bootstrap but before first login, restarting with the
      source removed or changed leaves the hash byte-for-byte unchanged: the
      original password remains valid and the changed value never becomes valid.
- [ ] Do not use `PasswordChangeRequired` as the password-initialization trigger.

### First login and existing installations

- [ ] Preserve `PasswordChangeRequired=true` on initial/partial bootstrap.
- [ ] Preserve existing middleware redirection and `/reset_password` workflow,
      password policy, bcrypt behavior, and transition to
      `PasswordChangeRequired=false`.
- [ ] A normal upgrade with an existing non-empty hash requires no bootstrap
      source and performs no user mutation.
- [ ] After the first password change, restart requires no FILE/ENV and does not
      re-enter bootstrap.
- [ ] Username may be logged as non-secret operational information, but no
      password value may accompany it.

### Zero plaintext output and persistence

- [ ] Delete the `log.Infof` or any equivalent plaintext password emission.
- [ ] A distinctive synthetic bootstrap password appears zero times in captured
      logger output, stderr, stdout, configured application log, Docker logs,
      Ansible output, generated configuration, application-created files, and
      final container filesystem outside the operator-provided mount.
- [ ] Database assertions prove the logical plaintext appears in no relevant
      column and only a valid non-equal bcrypt hash is persisted.
- [ ] No error wrapping, panic, debug output, test failure, or length/hash
      fingerprint discloses the secret.
- [ ] Do not rely on changing log-file permissions, stdout/stderr selection,
      masking, base64, hardcoded defaults, deterministic generation,
      unauthenticated frontend delivery, or an output secret file.

### Ansible, Docker, and documentation

- [ ] Remove the Ansible tasks that scrape and print `gophish.log` for the
      initial password.
- [ ] Adapt existing Ansible conventions to provide the bootstrap password as an
      input secret file without displaying it. Any task/variable handling the
      value uses `no_log` where needed.
- [ ] Do not build a new Vault subsystem or commit a real/example credential.
- [ ] Document preferred Docker/container use with a read-only mounted secret
      file and
      `GOPHISH_INITIAL_ADMIN_PASSWORD_FILE=/run/secrets/...`.
- [ ] Do not bake the secret into Dockerfile/image/config.json or require it as a
      visible environment value in the preferred example.
- [ ] Document native use via a restricted input file and ENV only as a
      less-preferred compatibility fallback.
- [ ] Document FILE > ENV precedence, invalid-FILE fatal behavior, parsing,
      bounds, fresh fail-closed behavior, partial recovery, existing-install
      compatibility, restart stability, and forced first change.
- [ ] Explicitly document the security breaking change:
      fresh installation without an explicit initial administrator password
      source no longer auto-generates/logs a password and now exits non-zero.
- [ ] Upgrade notes state that an existing non-empty hash needs no source, while
      a historical admin with an empty hash requires FILE or ENV.

### Scope controls

- [ ] Do not change the global logger implementation or the `0644` mode in this
      PR. Record/retain that as a separate backlog.
- [ ] Do not modify `docker/run.sh`'s `cat config.json`; it is the next separate
      small security change.
- [ ] Do not modify API-key storage, session/API authentication, verifier schema,
      IMAP/SMTP/webhook encryption, general login architecture, TLS, or
      `events.details`.
- [ ] Do not create a general offline password-reset command.
- [ ] Do not remove forced password change or use the initial API token as a
      password.
- [ ] Add no dependency and leave `go.mod`, `go.sum`, `package.json`, and
      `yarn.lock` unchanged.

### Tests and validation

- [ ] Add focused Go tests for source priority, valid FILE, ENV fallback, missing
      source, invalid authoritative FILE with valid ENV present, read-once
      behavior, parsing, bcrypt bounds, hash marker, fresh atomicity, partial
      recovery, restart stability, first login, API-token independence, and
      zero logging.
- [ ] Cover FILE absent, unreadable, directory, empty, line-ending-only,
      oversized, invalid UTF-8, NUL, valid symlink, LF, CRLF, and space-before-
      newline cases without placing real credentials in fixtures.
- [ ] Cover ENV empty/missing, too short, over 72 bytes, valid bounds, and the
      same applicable policy behavior.
- [ ] Prove fresh no-source and invalid-source failure creates zero new user rows.
- [ ] Prove fresh success stores bcrypt only and restart before reset neither
      reads a changed source nor modifies the hash.
- [ ] Prove existing `Hash != ""` plus `PasswordChangeRequired=true` starts
      without source and remains unchanged.
- [ ] Prove historical `Hash == ""` recovery succeeds once and fails safely when
      its source is unavailable.
- [ ] Test the actual first-login forced-change flow and restart after completion.
- [ ] Capture logger/stdout/stderr/configured file output and assert the synthetic
      secret is absent. A supervisor/journald deployment is not required because
      stderr capture proves that boundary.
- [ ] Build/run the real container with a mounted synthetic secret file and
      assert success, bcrypt-only DB storage, no secret in `docker logs`, config,
      image/final filesystem, or application logs.
- [ ] Re-run API-key bootstrap tests to prove
      `GOPHISH_INITIAL_ADMIN_API_TOKEN` behavior is unchanged.
- [ ] Run `./scripts/verify.sh`, `go test ./...`, `go test -race ./...`,
      `go vet ./...`, and `go build ./...`.
- [ ] Run relevant Docker, browser only if affected, `govulncheck`, `gosec`,
      Gitleaks, actionlint, zizmor, Yarn audit, and Retire.js checks. Classify
      pre-existing findings honestly and add no blanket suppression.
- [ ] `git diff --check`, formal self-review, dependency-diff check, and
      reproducibility checks pass before push.

### Delivery and review

- [ ] Create/update focused secure-bootstrap operations documentation and a
      formal self-review mapping every criterion to evidence.
- [ ] Every commit is a signed Conventional Commit with a why-focused body,
      required Goal role marker/trailers where applicable, and verified GOOD
      signature. Stop on signing failure.
- [ ] Push only after full local gates.
- [ ] PR description documents threat, legacy behavior/restart leak, new sources
      and priority, parser/bcrypt bounds, ENV compatibility, fresh/upgrade/
      partial behavior, atomicity, hash marker, forced change, zero logging,
      Docker/Ansible, breaking change, dependencies, tests, scanners, and
      self-review.
- [ ] Merge only after Goal Inspector PASS, independent security/code review
      PASS, all CI green, GitHub-verified signatures, no conflict, and no
      unresolved review conversation.
- [ ] After merge, switch to `main`, fast-forward from `origin/main`, verify a
      clean tree, and stop without beginning logger-mode, config-dump, API-key,
      or `events.details` work.

## Scope Boundaries

**In scope:**
- Initial administrator password source, one-time hash initialization, restart
  stability, removal from all logging sinks, Ansible bootstrap input, Docker/
  native documentation, targeted tests and operational evidence.
- Directly coupled atomicity and historical empty-hash recovery required to
  eliminate generated/logged passwords safely.

**Out of scope:**
- Global logger permissions/rotation/refactor.
- Docker runtime config dump.
- API keys, API/session authentication, HMAC verifier/pepper.
- IMAP, SMTP, webhook credentials, TLS, general login redesign,
  `events.details`, or an offline password-reset command.
- Generated output secret files or new dependencies.

## Applicable Project Conventions

**Quality gate commands:**
- `./scripts/verify.sh`
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `go build ./...`
- Relevant container and security scanner commands listed above.

**Commit convention:**
- Signed Conventional Commits using `git commit -S`, with the reason in the body.
- Builder/Inspector `[B]`/`[I]` markers required by the Goal workflow.
- `Assisted-by: Claude:Sonnet-4.6` for Builder and
  `Assisted-by: Claude:Haiku-4.5` for Inspector.
- `Co-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>`.
- Verify every signature with WSL-native Git/GPG and require `GOOD`.

**Guidelines:**
- `CLAUDE.md`
- `SECURITY.md`
- `CONTRIBUTING.md`
- `.github/PULL_REQUEST_TEMPLATE.md`
- No `AGENTS.md`, `CONSTITUTION.md`, `.agents/guidelines`, or
  `.github/guidelines` exist.

**Rules:**
- Never commit or push directly to `main`; one security branch and PR.
- Never place real/bootstrap secrets in source, tests, docs, config, images,
  logs, examples, or command output.
- Use synthetic distinctive test values only.
- Never weaken a security control or hide scanner/test failures.
- Preserve existing installs and first-login behavior; document the intentional
  fresh-install breaking change.
- No dependency is needed or allowed for this goal.
- Preserve Action SHA pinning, minimal permissions, timeouts, and CI protections.
- Full local gates, independent reviews, green CI, formal self-review, verified
  signatures, and no open thread before merge.
