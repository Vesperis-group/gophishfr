# Upstream synchronisation

GophishFR is a derivative of [Gophish](https://github.com/gophish/gophish).
This document is the **only** supported way to bring upstream changes in.

## Remotes

```bash
git remote -v
# origin    git@…:Vesperis-group/gophishfr.git   (fetch)
# origin    git@…:Vesperis-group/gophishfr.git   (push)
# upstream  https://github.com/gophish/gophish.git (fetch)
# upstream  DISABLED-NO-PUSH-UPSTREAM              (push)
```

If `upstream` is missing, add it with the push URL neutralised so an
accidental `git push upstream` cannot reach the Gophish project:

```bash
git remote add upstream https://github.com/gophish/gophish.git
git remote set-url --push upstream DISABLED-NO-PUSH-UPSTREAM
```

## Baseline

GophishFR imported the **full upstream history** via an unrelated-histories
merge in PR #1, at `upstream/master` commit `9561846` (Gophish 0.12.1+).

Because that history is present, an upstream sync is an **ordinary merge** —
no `--allow-unrelated-histories`, no artificial conflicts, and `git blame`
still points at the real upstream authors.

## Procedure

Never merge `upstream` into `main` directly. Always use a branch and a PR.

```bash
# 1. Start from a clean, up-to-date main
git switch main
git pull --ff-only origin main
git status --short                 # must be empty

# 2. Fetch upstream
git fetch upstream --prune --tags

# 3. Dedicated sync branch, dated
git switch -c chore/sync-upstream-$(date +%Y%m%d)

# 4. Review what is coming in BEFORE merging
git log --oneline main..upstream/master
git diff --stat main..upstream/master

# 5. Merge (signed)
git merge -S upstream/master
```

### Resolving conflicts

Conflicts are expected in files GophishFR has diverged on. Current known
divergences:

| File | GophishFR change | Resolution guidance |
|---|---|---|
| `README.md` | GophishFR banner prepended | Keep our banner, take upstream body below it |
| `SECURITY.md` | Reports go to GophishFR, not Gophish | **Always keep ours** — never restore upstream contact details |
| `CONTRIBUTING.md` | GophishFR process | Keep ours |
| `.github/workflows/**` | Hardened, SHA-pinned | Keep ours; port genuinely new upstream jobs manually |
| `Dockerfile` | Hardened, multi-stage | Keep ours; port upstream runtime changes manually |
| `go.mod` / `go.sum` | Upgraded, vulnerabilities fixed | **Never downgrade** to upstream versions |

Removed upstream files that must **not** be resurrected by a sync:

- `ISSUE_TEMPLATE.md` (replaced by `.github/ISSUE_TEMPLATE/`)

### Validate

```bash
./scripts/verify.sh
```

The full CI must run and be green on the sync PR — a sync is not a
"documentation" change, it can introduce vulnerabilities and regressions.

### Ship

```bash
git push -u origin chore/sync-upstream-YYYYMMDD
```

Then open a PR, self-review the diff, wait for green CI, merge, and:

```bash
git switch main
git pull --ff-only origin main
```

## Checklist

- [ ] `main` clean and up to date before starting
- [ ] Incoming commits reviewed before merging
- [ ] Merge commit signed (`git merge -S`)
- [ ] No security control weakened by the merge
- [ ] No dependency downgraded
- [ ] No upstream contact detail restored in `SECURITY.md` / `CONTRIBUTING.md`
- [ ] `govulncheck` no worse than before the sync
- [ ] Full CI green
- [ ] Merged via PR — never `upstream` → `main` directly
