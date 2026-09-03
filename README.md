# GophishFR

![GophishFR logo](static/images/gophish_purple.png)

[![CI](https://github.com/Vesperis-group/gophishfr/actions/workflows/ci.yml/badge.svg)](https://github.com/Vesperis-group/gophishfr/actions/workflows/ci.yml)

GophishFR is an independently maintained security awareness simulation platform
for authorized training and assessment campaigns.

> [!IMPORTANT]
> Use GophishFR only on systems and recipients for which you have explicit
> authorization. You are responsible for complying with applicable laws,
> policies, and consent requirements.

## Install

Download an archive for your platform from the
[GophishFR releases](https://github.com/Vesperis-group/gophishfr/releases),
extract it, and run the `gophishfr` binary.

On first start, the application writes the temporary administrator credentials
to the console. Open <https://localhost:3333>, sign in with those credentials,
and change the password immediately.

## Build from source

Building GophishFR requires the Go version declared in [`go.mod`](go.mod).

```sh
git clone https://github.com/Vesperis-group/gophishfr.git
cd gophishfr
go build -o gophishfr .
./gophishfr
```

The default [`config.json`](config.json) stores the SQLite database and logs
relative to the working directory. Back up existing data before changing paths
or deployment layouts.

## Docker

Build the container image from the checked-out source:

```sh
docker build --tag gophishfr .
docker run --rm -it \
  -p 3333:3333 \
  -p 8080:80 \
  gophishfr
```

The container keeps `/opt/gophish` as its internal data directory for
compatibility with existing deployments. Mount persistent configuration,
database, and log storage according to your environment before using the image
beyond local evaluation.

## Development

Run the complete local quality gate before submitting changes:

```sh
./scripts/verify.sh
```

Frontend assets use the Node version pinned in [`.nvmrc`](.nvmrc), Yarn Classic
as declared by `package.json`, and the committed `yarn.lock`. Enable Corepack
once for the selected Node installation, then use an immutable install:

```sh
nvm use
corepack enable
yarn install --frozen-lockfile --non-interactive
yarn build
```

The frontend browser smoke baseline has a separate explicit command because it
installs and launches Chromium. See
[`docs/FRONTEND_BROWSER_TESTS.md`](docs/FRONTEND_BROWSER_TESTS.md).
The HTML source editor and isolated preview security model are documented in
[`docs/HTML_EDITOR_MIGRATION.md`](docs/HTML_EDITOR_MIGRATION.md).
The limits enforced when importing group members from a CSV file are documented
in [`docs/GROUP_IMPORT_LIMITS.md`](docs/GROUP_IMPORT_LIMITS.md).
IMAP keyring setup, offline credential migration, and safe rollback are
documented in
[`docs/IMAP_CREDENTIAL_ENCRYPTION.md`](docs/IMAP_CREDENTIAL_ENCRYPTION.md).
SMTP sending-profile encryption, write-only API behavior, offline migration,
and rollback are documented in
[`docs/SMTP_CREDENTIAL_ENCRYPTION.md`](docs/SMTP_CREDENTIAL_ENCRYPTION.md).

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution requirements and
[SECURITY.md](SECURITY.md) for responsible vulnerability reporting.

## Issues

Report GophishFR bugs and documentation gaps in the
[GophishFR issue tracker](https://github.com/Vesperis-group/gophishfr/issues).
Do not report them to the historical Gophish project.

## History and attribution

GophishFR is derived from
[Gophish](https://github.com/gophish/gophish), originally created by Jordan
Wright. GophishFR is not affiliated with or endorsed by the Gophish project.
The preserved Git history, [NOTICE](NOTICE), and [LICENSE](LICENSE) document the
original project's provenance, copyright, and MIT license terms.
