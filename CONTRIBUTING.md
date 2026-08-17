# Contributing to GophishFR

Thanks for your interest in GophishFR!

GophishFR is a derivative of [Gophish](https://github.com/gophish/gophish).
Before contributing, please read [NOTICE](NOTICE) and [LICENSE](LICENSE).

> **Contributing upstream instead.** If your change is a general Gophish
> improvement rather than a GophishFR-specific one, please consider sending it
> to [the Gophish project](https://github.com/gophish/gophish) so every
> downstream user benefits. We will pick it up on our next upstream sync.

## Security issues

**Do not open a public issue for a suspected vulnerability.** Follow
[SECURITY.md](SECURITY.md) and use GitHub Private Vulnerability Reporting.

## Licensing of contributions

By submitting a contribution you agree that it is licensed under the MIT
license of this repository (see [LICENSE](LICENSE)), and that you have the
right to submit it.

The upstream Gophish contributor license agreements under `doc/` are kept for
historical attribution. **They do not apply to GophishFR contributions.**

## Ground rules

The full, binding engineering rules live in [CLAUDE.md](CLAUDE.md). They apply
to humans and AI agents alike. The essentials:

- Never work directly on `main`. One change = one branch = one pull request.
- **Every commit must be signed** (`git commit -S`) and verified.
- Use [Conventional Commits](https://www.conventionalcommits.org/):
  `feat:`, `fix:`, `security:`, `ci:`, `build:`, `test:`, `refactor:`,
  `docs:`, `chore:`.
- No secrets, anywhere. Examples must be obviously fake.
- Behaviour changes need tests. Bug fixes need a regression test.
- New dependencies must be justified (need, alternative, security impact,
  maintenance impact) and pinned.
- Never disable or blanket-ignore a security check to make CI pass.

## Getting started

```bash
git clone git@github.com:Vesperis-group/gophishfr.git
cd gophishfr
git remote add upstream https://github.com/gophish/gophish.git
git remote set-url --push upstream DISABLED-NO-PUSH-UPSTREAM
```

Build and test:

```bash
go build ./...
go test ./...
```

Run the same gates CI runs, locally:

```bash
./scripts/verify.sh
```

> **Bootstrap note.** This script is being added by the bootstrap PR sequence.
> Until it lands, run `gofmt -l .`, `go vet ./...` and `go test ./...`.

## Submitting a pull request

```bash
git switch main
git pull --ff-only origin main
git switch -c feat/short-description
# ... work ...
git commit -S -m "feat: short description"
git log -1 --show-signature      # confirm the signature
git push -u origin feat/short-description
```

Then open a PR and fill in the template. A PR is ready to merge only when:

- CI is fully green
- every commit is signed and verified
- the diff has been reviewed and no conversation is left unresolved
- there are no merge conflicts

## Responsible use

GophishFR is a phishing simulation tool for **authorised** security awareness
training and penetration testing. Contributions that exist only to make abuse
easier, or to evade detection by the people being protected, will be declined.

## Questions

Open a [GitHub issue](https://github.com/Vesperis-group/gophishfr/issues) or a
discussion. Please keep exchanges in English or French, and courteous.