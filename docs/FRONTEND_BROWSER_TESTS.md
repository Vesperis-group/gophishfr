# Frontend browser smoke tests

The browser smoke suite is a small functional baseline for the legacy frontend.
It protects the maintained build pipeline and future dependency changes without
introducing broad end-to-end coverage.

## Tooling

The suite uses Playwright with Chromium. Playwright was selected because its
package pins a compatible browser revision, works with the Node version in
`.nvmrc`, and adds only three Node packages. A Go browser driver would still
require an independently installed, unpinned system browser; Cypress would add
a larger runtime for the same smoke-test scope.

`@playwright/test` is pinned to 1.62.1. The authoritative Yarn audit currently
reports no dependency advisory; see
[`FRONTEND_DEPENDENCY_BASELINE.md`](FRONTEND_DEPENDENCY_BASELINE.md).

Install the immutable Node dependency graph and the pinned Chromium revision:

```sh
corepack yarn install --frozen-lockfile --non-interactive
yarn playwright install chromium
```

Run the suite:

```sh
yarn test:browser
```

The harness currently supports Linux and WSL, matching the CI environment.
The command rebuilds the frontend assets, creates an isolated SQLite database,
starts the real admin router on an ephemeral loopback port, runs Chromium, and
cleans up the server, browser output, and temporary database even when a test
fails. Browser system packages may need to be installed once on Linux with
`yarn playwright install-deps chromium`.

## Coverage

The suite verifies:

- login rendering, CSS application, jQuery-global absence, vendor globals,
  synthetic authentication, and the dashboard redirect;
- dashboard chart rendering, values, labels, and campaign navigation;
- campaign-result doughnut and timeline rendering, tooltip data, horizontal
  zoom, and zoom reset;
- campaign navigation, Bootstrap tabs and modals, the native `<select>`
  controls, the Tom Select group picker, the native datetime controls, and the
  campaign launch payload contract;
- group DataTables rendering and client-side target entry;
- template DataTables rendering, CKEditor initialization, and Bootstrap tabs;
- User Management rendering and the bundled zxcvbn password-strength behavior;
- native fetch transport contracts, including handled HTTP, parsing, and
  network failures with no jQuery runtime;
- request and settlement parity for the 11 audited asynchronous wrappers, plus
  jQuery-free execution for the 10 migrated wrappers;
- success, failure, and cleanup behavior for campaign results and reporting,
  campaign group setup, user and webhook writes, API-key reset, and IMAP
  validation;
- successful loading of critical CSS, JavaScript, image, and font assets;
- absence of unexpected JavaScript exceptions, console errors, failed local
  requests, and HTTP error responses.

It deliberately does not launch campaigns, send email, exercise IMAP, compare
pixels, or snapshot generated HTML and bundles.

## Isolation

The Go harness uses a temporary SQLite file and synthetic credentials and
fixtures. The sending profile points to the closed local address
`127.0.0.1:1`, the background worker is never started, and no action capable of
sending email is exercised.

Playwright rejects a non-loopback base URL. During the test, all requests are
restricted to the ephemeral local server. The two existing Google Fonts
stylesheet requests are fulfilled with empty local responses rather than sent
to the Internet; every other external request fails the suite. Routing is
applied to the complete browser context, service workers are blocked, and
non-loopback WebSockets are rejected.

These tests are intentionally smoke tests, not an exhaustive E2E or
visual-regression suite.
