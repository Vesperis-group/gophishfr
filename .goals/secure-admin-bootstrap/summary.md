# Secure Initial Administrator Bootstrap Summary

## Outcome

The goal passed independent inspection, security review, and final code review
after two Builder iterations. Initial administrator passwords are now explicit
operator inputs rather than generated/logged outputs, fresh initialization is
hash-before-insert, historical empty-hash recovery is one-time, and initialized
installations never reread or rehash bootstrap sources.

## Acceptance Criteria Achieved

- `GOPHISH_INITIAL_ADMIN_PASSWORD_FILE` is the preferred authoritative source;
  invalid configured files fail without ENV fallback.
- `GOPHISH_INITIAL_ADMIN_PASSWORD` remains a lower-priority compatibility
  fallback.
- Fresh or historical empty-hash bootstrap without a valid source fails closed.
- Automatic generated-password fallback and plaintext logging are removed.
- FILE and ENV share 8–72 byte validation, UTF-8/NUL checks, and non-truncating
  bcrypt behavior.
- A fresh admin's bcrypt hash is prepared before insertion, preventing partial
  user creation on source/hash failure.
- Existing `Hash == ""` state recovers exactly once; `Hash != ""` never rereads
  a source even while `PasswordChangeRequired` remains true.
- Restart before first login preserves the original hash/password.
- Forced first password change and initial API-token behavior remain unchanged.
- Distinctive test secrets are absent from logger, stderr/stdout, application
  logs, Docker logs, configuration, database plaintext, and image filesystem.
- Ansible no longer scrapes logs; it provisions a mode-0400 input file under
  `no_log`, with an overridable role default and matching systemd environment.
- Existing initialized deployments require no bootstrap source.
- No output secret file, dependency, logger-mode, Docker config-dump, API-key,
  or credential-encryption change is included.

## Iteration History

1. Builder iteration 1 implemented secure bootstrap, focused tests, container
   lifecycle, Ansible changes, documentation, and self-review in commit
   `5562f54f4a553fcb2b005a2be4f7c5807724efb0`.
2. Goal Inspector iteration 1 returned PASS in commit
   `f07dba45a312df16a524a0ad343e5e2089857ea7`.
3. Security review found the empty Ansible value under role `vars/` shadowed
   inventory/play/Vault overrides, preventing real provisioning.
4. Builder iteration 2 moved the value to role `defaults/`, added an executable
   precedence/structure regression, and reran all gates in commit
   `b9c3e69edfa64a50d75a1f65d43b9b9bbc0fc903`.
5. Goal Inspector iteration 2 returned PASS in commit
   `50aeb20c266c1c96f29473d2e343f3f727dd8fc4`.
6. Independent security and complete-diff code re-reviews both returned PASS.

## Review Resolution

Ansible role defaults now permit secure inventory/play/Vault input while an
unset value skips provisioning for initialized systems. Secret tasks remain
censored and create a service-readable mode-0400 file. Structural regression
coverage prevents a future high-precedence variable from silently disabling the
documented bootstrap path.

## Recommendations

- Keep global log-file mode hardening in its own PR.
- Remove the Docker runtime `config.json` dump next, without combining API-key
  or logger refactors.
- Follow with API-key session decoupling and verifier work as separately reviewed
  changes.
