# Security Review Feedback — Iteration 1

## Verdict: FAIL

An independent security specialist found one high-confidence deployment
misconfiguration within the immutable goal.

## Finding — Ansible role variable blocks secure overrides

**Severity:** Medium
**Confidence:** 10/10
**Affected path:** `ansible-playbook/roles/gophish/vars/main.yml`

The role defines the new bootstrap-password value as empty under `vars/`.
Ansible role vars have higher precedence than inventory and play variables, so
the documented inventory/Vault value cannot override it. The task that writes
the mounted input file is skipped, the systemd environment is omitted, and a
fresh or historical empty-hash installation fails closed instead of booting.

## Required correction

- Move the empty, non-secret default to
  `ansible-playbook/roles/gophish/defaults/main.yml`.
- Remove the overriding definition from `vars/main.yml`.
- Preserve `no_log` on every task that handles the secret.
- Add an Ansible-focused regression or deterministic structural test proving an
  inventory/play override reaches the secret-file task and service environment.
- Prove the unset default still skips secret-file provisioning for already
  initialized installations.
- Verify file ownership/mode and service-user readability remain correct.
- Run syntax/lint checks available in the repository/environment and the real
  bootstrap/container lifecycle.
- Preserve all original bootstrap, zero-logging, scope, and dependency criteria.
