<!--
  Read CLAUDE.md before opening this PR.
  Security issue? Do NOT open a PR — see SECURITY.md.
-->

## Objective

<!-- What problem does this solve, and why now? -->

## Changes

<!-- What changed, and why it was done this way. Call out anything surprising. -->

## Behaviour impact

- [ ] No change to existing behaviour
- [ ] Behaviour changes — described below, and covered by tests

<!-- If behaviour changes, describe the before/after explicitly. -->

## Dependencies

- [ ] No dependency added, removed or upgraded

<!--
  Otherwise, for each dependency, justify:
  - the need it covers
  - the alternative considered
  - the security impact
  - the maintenance impact (is the project alive? licence?)
  and confirm the version is pinned and the lockfile updated.
-->

## Validation

<!-- Paste the actual results. Never claim a check you did not run. -->

| Check | Result |
|---|---|
| `./scripts/verify.sh` | |
| `go test ./...` | |
| Frontend build | |
| Other | |

## Self-review

- [ ] I read the full diff (`git diff main...HEAD`) and `git diff --check` is clean
- [ ] Every commit is signed and verified (`git log --show-signature`)
- [ ] Commits follow Conventional Commits
- [ ] No secret, token, credential or personal data introduced — examples are obviously fake
- [ ] No security control weakened, disabled or blanket-ignored
- [ ] Any new GitHub Action is pinned to a full commit SHA with a version comment
- [ ] Workflow `permissions:` are minimal
- [ ] New behaviour and bug fixes are covered by tests
- [ ] Documentation updated if this change affects it
- [ ] Historical attribution (`LICENSE`, `NOTICE`) preserved

## Risks and follow-ups

<!-- Residual risk, debt deliberately kept, what the next PR should do. -->
