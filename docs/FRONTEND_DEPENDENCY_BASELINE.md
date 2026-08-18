# Frontend dependency and build baseline

This baseline was remeasured on 2026-08-18 with Node.js 24.19.0 and Yarn
Classic 1.22.22. The frontend build remains development-only: Node packages are
not copied into release archives or the runtime container.

## Measurement

`yarn.lock` remains the authoritative dependency graph. The locked graph and the
package-level npm comparison were measured with:

```sh
yarn install --frozen-lockfile --non-interactive
yarn audit --json

# In a temporary directory containing package.json only:
npm install --package-lock-only --ignore-scripts --no-audit
npm audit --json
```

No `package-lock.json`, `resolutions`, audit suppression, or automatic audit fix
was added.

| Metric | Before modernization | After modernization |
| --- | ---: | ---: |
| npm vulnerable packages | 22 | 0 |
| npm critical | 0 | 0 |
| npm high | 10 | 0 |
| npm moderate | 7 | 0 |
| npm low | 5 | 0 |
| npm production | 0 | 0 |
| npm development | 22 | 0 |
| Yarn vulnerable paths | 27 | 0 |
| Yarn unique advisories | 7 | 0 |
| Yarn unique critical | 1 | 0 |
| Yarn unique high | 4 | 0 |
| Yarn unique moderate | 1 | 0 |
| Yarn unique low | 1 | 0 |
| Yarn audit dependency count | 744 | 75 |
| Yarn lock selectors | 696 | 75 |

## Advisory inventory before modernization

All findings were reachable only while installing or running the build
toolchain. They did not execute in the Go server or runtime image, but remained
relevant to CI and developer hosts processing repository content.

| Package | Advisory | Severity | Parent and role | Patched version | Decision |
| --- | --- | --- | --- | --- | --- |
| `set-value` 0.4.3 | `GHSA-4g88-fppr-53pp` / `CVE-2019-10747` | critical | Gulp/Webpack watcher and glob chains through `micromatch`, `snapdragon`, `cache-base`, and `union-value` | 2.0.1 | Remove the obsolete chains |
| `set-value` 0.4.3 | `GHSA-4jqc-8m5r-9rpr` / `CVE-2021-23440` | high | Same Gulp/Webpack watcher and glob chains | 2.0.1 | Remove the obsolete chains |
| `braces` 2.3.2 | `GHSA-grv7-fg5c-xmjg` / `CVE-2024-4068` | high | Gulp/Webpack file matching through `micromatch` | 3.0.3 | Remove Gulp and upgrade Webpack |
| `serialize-javascript` 4.0.0 | `GHSA-5c6j-r48x-rmvq` | high | Webpack 4 minification through `terser-webpack-plugin` | 7.0.3 | Upgrade Webpack |
| `terser` 5.9.0 | `GHSA-4wf5-vphf-c2xc` / `CVE-2022-25858` | high | JavaScript minification through abandoned `gulp-uglify-es` | 5.14.2 | Replace the wrapper with Terser 5.50.0 direct |
| `micromatch` 3.1.10 | `GHSA-952p-6rrq-rcjv` / `CVE-2024-4067` | moderate | Gulp/Webpack watcher and glob matching | 4.0.8 | Remove Gulp and upgrade Webpack |
| `elliptic` 6.6.1 | `GHSA-848j-6mx2-7j84` / `CVE-2025-14505` | low | Webpack 4 Node crypto polyfills | No patched release | Upgrade Webpack, which no longer installs the polyfill chain |

The migration removes every affected dependency chain. No advisory is
suppressed or accepted as a false positive.

## Toolchain migration

The old normal build invoked only Gulp. Its three tasks used fixed source lists
to concatenate and minify vendored JavaScript, minify application scripts, and
minify then concatenate stylesheets. No watch task was used. Webpack and Babel
were installed and configured but were not called by `yarn build`, CI, or
Docker.

That disconnected setup also caused `passwords.min.js` to retain a bare
`import zxcvbn` statement while templates loaded it as a classic script. The
password-strength code was therefore not executable after a normal build.

| Component | Before | Decision | After |
| --- | --- | --- | --- |
| Webpack | 4.47.0, disconnected three-entry config | `UPGRADE_MAJOR` and reduce scope | 5.109.2, one `passwords.js` entry invoked through the Node API |
| webpack-cli | 3.3.12, unused | `REMOVE` | No CLI; the build calls Webpack programmatically |
| Terser | 5.9.0 through abandoned `gulp-uglify-es` | `REPLACE` | Direct exact dependency 5.50.0 |
| Gulp | 4.0.1 | `REMOVE` | Fixed ordered lists and filesystem operations in `scripts/build-frontend.js` |
| Gulp plugins | `gulp-clean-css`, `gulp-concat`, `gulp-rename`, `gulp-uglify-es` | `REMOVE` | Direct CleanCSS/Terser APIs and Node filesystem APIs |
| Babel | Babel 7 core/preset and babel-loader 8, disconnected | `REMOVE` | No dormant transpilation configuration |
| CSS minifier | CleanCSS 4.2.1 through Gulp | `UPGRADE_MAJOR` | Direct exact dependency 5.3.3 |
| CSS/assets | Per-file level-1 CSS minification, then fixed-order concatenation | `KEEP` | Same ordering and optimization level |

Application scripts remain classic scripts. Terser uses its non-module defaults,
so top-level function names referenced by legacy inline handlers are not
mangled. The vendor list retains its original fixed order and newline
separators. Webpack is the sole producer of `passwords.min.js`; every other
application script remains independently minified.

## Direct dependency changes

| Package | Before | After | Reason |
| --- | --- | --- | --- |
| `clean-css` | 4.2.1 | 5.3.3 exact | Maintained direct CSS minifier |
| `webpack` | 4.47.0 | 5.109.2 exact | Remove vulnerable Webpack 4 chains and bundle the real module import |
| `terser` | transitive 5.9.0 | 5.50.0 exact | Replace the abandoned Gulp wrapper with the maintained API |
| `@babel/core` | 7.29.7 | removed | Disconnected build dependency |
| `@babel/preset-env` | 7.4.5 | removed | Disconnected build dependency |
| `babel-loader` | 8.0.6 | removed | Disconnected build dependency |
| `gulp` | 4.0.1 | removed | Three simple non-watching tasks are implemented directly |
| `gulp-cli` | 2.2.0 | removed | No Gulp runtime remains |
| `gulp-clean-css` | 4.0.0 | removed | Direct CleanCSS API replaces the wrapper |
| `gulp-concat` | 2.6.1 | removed | Ordered string concatenation replaces the wrapper |
| `gulp-rename` | 1.4.0 | removed | Output names are explicit |
| `gulp-uglify-es` | 3.0.0 | removed | Abandoned and pinned to vulnerable Terser |

`terser` is the only new direct dependency, but it replaces an existing
transitive minifier and its abandoned wrapper. Calling the maintained API
directly avoids another stream plugin and reduces maintenance and supply-chain
surface.

## Generated assets

The committed assets were rebuilt because the minifiers changed. Comparison
against the previous baseline is informational; reproducibility is enforced
between clean builds of the new state.

| Metric | Before | After |
| --- | ---: | ---: |
| Generated files | 15 | 16 |
| Aggregate bytes | 1,395,926 | 2,209,800 |
| Aggregate SHA-256 manifest | `5411d7336d29c01e3461df0efe8971deab9a22fb4d7812a24186609280d4f451` | `6389202e8d0ae7d32e14fc90bf1ad580e92314113a6b2bffe56e4e9dd664ec20` |
| `vendor.min.js` | 983,831 bytes | 981,029 bytes |
| `gophish.css` | 329,304 bytes | 329,349 bytes |
| `passwords.min.js` | 1,008 bytes, invalid bare import | 819,870 bytes, bundled `zxcvbn` |

The additional file is `passwords.min.js.LICENSE.txt`, copied from the zxcvbn
package so its MIT notice ships with the bundled code. The 813,874-byte total
increase is attributable to the previously missing zxcvbn bundle and its
license; the other generated assets are collectively smaller.

Two consecutive clean builds produced identical per-file hashes and the same
aggregate manifest hash shown above. File names and template references are
unchanged.

## Compatibility validation

The Playwright smoke baseline still validates login, navigation, representative
admin pages, Bootstrap, DataTables, Select2, the datetime picker, CKEditor,
critical assets, failed requests, console errors, and JavaScript exceptions. It
now also opens User Management, loads both the users and password bundles, and
verifies that typing a synthetic password updates the zxcvbn strength indicator.

Webpack reports the expected 801 KiB password bundle performance warning. It is
kept visible rather than suppressed. Lazy-loading or replacing zxcvbn would be a
separate application-performance and behavior change, not a build-toolchain
remediation.

## Remaining frontend debt

The npm and Yarn audits report zero known dependency vulnerabilities. Remaining
debt is non-advisory:

- Yarn Classic emits Node's `DEP0169` deprecation warning under Node 24.
- The repository still ships old vendored browser libraries that are outside
  the package-manager graph.
- zxcvbn 4.4.2 is old and produces a large bundle, although it has no current
  npm/Yarn advisory.

These items require separate, behavior-focused work and are not hidden or
suppressed by this migration.
