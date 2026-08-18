# Frontend dependency vulnerability baseline

This baseline was remeasured on 2026-08-18 with Node.js 24.19.0 and Yarn
Classic 1.22.22. This pull request applies only compatible patch/minor updates,
safe transitive refreshes within parent ranges, and removal of unused build
dependencies. It does not migrate Gulp or Webpack.

## Measurement

`yarn.lock` remains the authoritative dependency graph. The locked graph was
measured with:

```sh
yarn install --frozen-lockfile --non-interactive
yarn audit --json
```

The npm package-level comparison was reproduced outside the repository from
`package.json` alone:

```sh
npm install --package-lock-only --ignore-scripts --no-audit
npm audit --json
```

No `package-lock.json`, `resolutions`, audit suppression, or automatic audit fix
was added.

| Metric | Before | After |
| --- | ---: | ---: |
| npm vulnerable packages | 25 | 22 |
| npm high | 13 | 10 |
| npm moderate | 7 | 7 |
| npm low | 5 | 5 |
| npm critical | 0 | 0 |
| npm production | 0 | 0 |
| npm development | 25 | 22 |
| npm direct | 5 | 4 |
| npm transitive | 20 | 18 |
| Yarn vulnerable paths | 251 | 27 |
| Yarn unique advisories | 70 | 7 |
| Yarn unique critical | 12 | 1 |
| Yarn unique high | 32 | 4 |
| Yarn unique moderate | 16 | 1 |
| Yarn unique low | 10 | 1 |

The previous document classified the npm findings as 4 direct and 21
transitive. Reproduction showed 5 direct findings (`gulp`, `gulp-cli`, `jshint`,
`webpack`, and `webpack-cli`) and 20 transitive findings. The package and
severity totals were unchanged.

## Corrected now

The normal Gulp build produced byte-identical JavaScript and CSS before and
after these changes.

### Direct dependencies

The removals and direct patch/minor updates below are classified
`A - SAFE_NOW`. Their affected transitive trees are
`B - SAFE_PARENT_UPGRADE`.

| Package | Before | After | Method |
| --- | --- | --- | --- |
| `@babel/core` | 7.4.5 | 7.29.7 | Compatible Babel 7 minor update |
| `webpack` | 4.32.2 | 4.47.0 | Latest Webpack 4 minor |
| `webpack-cli` | 3.3.2 | 3.3.12 | Latest Webpack CLI 3 patch |
| `gulp-babel` | 8.0.0 | removed | Required but never used by `gulpfile.js` |
| `gulp-jshint` | 2.1.0 | removed | No lint task or import exists |
| `gulp-wrap` | 0.15.0 | removed | No task or import exists |
| `jshint` | 2.13.6 | removed | No lint task or script exists |
| `jshint-stylish` | 2.2.1 | removed | No lint task or import exists |

The Babel preset and loader remain because `webpack.config.js` uses them. The
Webpack dependencies remain because that configuration is still present, even
though the normal `yarn build` command currently invokes only Gulp.

### Compatible transitive refreshes

Yarn Classic does not update transitive entries with `yarn upgrade` unless they
are direct dependencies. The affected lock entries were therefore re-resolved
only where every parent already accepted the new version. No forced resolution
was added.

| Package | Before | After |
| --- | --- | --- |
| `@babel/helpers` | 7.4.4 | 7.29.7 |
| `@babel/traverse` | 7.4.5 | 7.29.8 |
| `ajv` | 6.12.6 | 6.15.0 |
| `ansi-regex` | 3.0.0 | 4.1.1 |
| `bn.js` | 4.12.0 | 4.12.5 / 5.2.5 |
| `brace-expansion` | 1.1.11 | 1.1.18 |
| `browserify-sign` | 4.0.4 | 4.2.6 |
| `cipher-base` | 1.0.4 | 1.0.7 |
| `cross-spawn` | 6.0.5 | 6.0.6 |
| `debug` | 4.1.1 | 4.4.3 |
| `decode-uri-component` | 0.2.0 | 0.2.2 |
| `elliptic` | 6.5.4 | 6.6.1 |
| `es5-ext` | 0.10.49 | 0.10.64 |
| `fsevents` | 1.2.8 | 1.2.13 / 2.3.3 |
| `json5` | 1.0.1 / 2.1.0 | 1.0.2 / 2.2.3 |
| `kind-of` | 6.0.2 | 6.0.3 |
| `loader-utils` | 1.2.3 | 1.4.2 |
| `lodash` | 4.17.21 / 4.17.23 | 4.18.1 |
| `minimatch` | 3.0.4 | 3.1.5 |
| `minimist` | 1.2.5 | 1.2.8 |
| `pbkdf2` | 3.0.17 | 3.1.6 |
| `semver` | 5.7.0 / 6.1.1 | 5.7.2 / 6.3.1 |
| `set-value` | 2.0.0 | 2.0.1 |
| `sha.js` | 2.4.11 | 2.4.12 |
| `y18n` | 4.0.0 | 4.0.3 |
| `yargs-parser` | 11.1.1 | 13.1.2 |

These updates and removals eliminate 63 unique Yarn advisories: 11 critical,
28 high, 15 moderate, and 9 low. The affected packages include Babel helpers,
the legacy Webpack crypto polyfills, JSHint dependencies, old file-watcher
dependencies, and vulnerable parsing/minimization support packages.

Notable advisories corrected include:

- `GHSA-67hx-6x53-jw92` (`@babel/traverse`)
- `GHSA-cpq7-6gpm-g9rc` (`cipher-base`)
- `GHSA-76p3-8jx3-jpfq` (`loader-utils`)
- `GHSA-xvch-5gv4-984h` (`minimist`)
- `GHSA-h7cp-r72f-jxh6` and `GHSA-v62p-rq8g-8h59` (`pbkdf2`)
- `GHSA-95m3-7q98-8xr5` (`sha.js`)
- `GHSA-x9w5-v3q2-3rhw` (`browserify-sign`)
- `GHSA-23c5-xmqv-rm74` and related `minimatch` advisories
- `GHSA-hxcc-f52p-wc94` (`serialize-javascript`)
- `GHSA-p9pc-299p-vxgp` (`yargs-parser`)

## Remaining intentionally

All remaining findings are development/build-only. The Node dependency tree is
not copied into release archives or the runtime container, and the Go server
does not load it.

| Package | Severity | Dependency chain | Classification | Reason for deferral |
| --- | --- | --- | --- | --- |
| `set-value` 0.4.3 | critical, high | Gulp/Webpack -> micromatch 3 -> snapdragon -> cache-base -> union-value -> set-value | `C - MAJOR_MIGRATION_REQUIRED` | The parent requires the incompatible 0.x API; correction requires replacing the micromatch 3 chain. |
| `braces` 2.3.2 | high | Gulp/Webpack -> micromatch 3 -> braces | `C - MAJOR_MIGRATION_REQUIRED` | Fixed in braces 3, outside the parent range. |
| `serialize-javascript` 4.0.0 | high | Webpack 4 -> terser-webpack-plugin 1 -> serialize-javascript | `C - MAJOR_MIGRATION_REQUIRED` | Fixed in 7.0.3, outside the plugin range. |
| `terser` 5.9.0 | high | gulp-uglify-es -> terser | `D - BUILD_ONLY_ACCEPTED_TEMPORARILY` | This code is reachable in every CI frontend build. Updating to the fixed 5.14.2+ rewrites every JavaScript bundle, and there are no browser regression tests proving those output changes safe. |
| `elliptic` 6.6.1 | low | Webpack 4 -> node-libs-browser -> crypto-browserify -> browserify-sign -> elliptic | `D - UNREACHABLE/BUILD_ONLY_ACCEPTED_TEMPORARILY` | The remaining advisory declares no patched release and the Webpack command is not part of the normal build. |
| `micromatch` 3.1.10 | moderate | Gulp/Webpack watcher chains -> micromatch | `C - MAJOR_MIGRATION_REQUIRED` | Fixed in micromatch 4, outside parent ranges. |

No finding was classified as a false positive. Build-only status limits product
runtime exposure, but CI and developer hosts still process untrusted repository
content, so these findings remain security debt rather than accepted risk
without qualification.

## Next step

A dedicated `build/modernize-frontend-toolchain` pull request is justified. It
should first add browser-level regression tests for the generated assets, then
replace or upgrade the abandoned Gulp minifier and migrate the Gulp/Webpack
dependency chains that require major versions. That work must not be combined
with this compatibility-preserving remediation.
