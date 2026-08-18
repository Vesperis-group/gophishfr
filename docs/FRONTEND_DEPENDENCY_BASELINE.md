# Frontend dependency vulnerability baseline

This baseline was measured on 2026-08-18. It intentionally records existing
findings without changing dependency versions. Remediation belongs in the
follow-up `security/frontend-dependency-remediation` pull request.

## Scope and method

Yarn Classic 1.22.22 and the committed `yarn.lock` are the authoritative
dependency graph. The immutable install and Gulp build use Node.js 24.19.0.

For comparison with the previously reported npm count, npm 11.17.0 generated a
temporary package lock outside the repository:

```sh
npm install --package-lock-only --ignore-scripts --no-audit
npm audit --json
```

No `package-lock.json` was added to the repository and no audit fix command was
run.

The npm report contains 25 vulnerable packages: 13 high, 7 moderate, 5 low,
and no critical findings. All 25 are development dependencies; the sole
production dependency, `zxcvbn`, is unaffected. Four vulnerable packages are
direct dependencies and 21 are transitive.

Yarn Classic's audit endpoint reports a broader historical set for the locked
graph: 251 vulnerable paths across 70 unique GitHub advisories. The follow-up
security work must reconcile advisory-level Yarn results with npm's
package-level aggregation rather than treating the two totals as equivalent.

## Reachability

The affected packages run only while linting, bundling, minifying, or watching
frontend sources. They are not copied into the runtime container and are not
loaded by the Go server. There is therefore no identified direct runtime path
from an application request to these packages.

The findings still matter on developer and CI hosts. Crafted build inputs or a
compromised dependency could cause denial of service, execute code during a
trusted build, or alter generated assets. The immutable lockfile reduces
resolution drift but does not remediate known vulnerable code.

## npm package-level classification

| Package | Severity | Relationship | Direct parent | Primary build-time impact |
| --- | --- | --- | --- | --- |
| `braces` | high | transitive | `webpack` | Resource exhaustion |
| `chokidar` | high | transitive | `gulp` | Resource exhaustion |
| `glob-watcher` | high | transitive | `gulp` | File-watcher dependency risk |
| `gulp` | high | direct | `gulp` | Build runner dependency risk |
| `jshint` | high | direct | `jshint` | Linter dependency risk |
| `lodash` | high | transitive | `jshint` | Build-tool dependency risk |
| `micromatch` | high | transitive | `webpack` | Regular-expression denial of service |
| `minimatch` | high | transitive | `jshint` | Regular-expression denial of service |
| `serialize-javascript` | high | transitive | `webpack` | Build-time code serialization |
| `terser-webpack-plugin` | high | transitive | `webpack` | Minifier dependency risk |
| `watchpack` | high | transitive | `webpack` | File-watcher dependency risk |
| `watchpack-chokidar2` | high | transitive | `webpack` | File-watcher dependency risk |
| `webpack` | high | direct | `webpack` | Bundler dependency risk |
| `anymatch` | moderate | transitive | `gulp` | Regular-expression denial of service |
| `findup-sync` | moderate | transitive | `gulp-cli` | Build-tool dependency risk |
| `gulp-cli` | moderate | direct | `gulp-cli` | Build runner dependency risk |
| `liftoff` | moderate | transitive | `gulp-cli` | Build-tool dependency risk |
| `matchdep` | moderate | transitive | `gulp-cli` | Build-tool dependency risk |
| `readdirp` | moderate | transitive | `gulp` | Resource exhaustion |
| `webpack-cli` | moderate | direct | `webpack-cli` | Bundler CLI dependency risk |
| `browserify-sign` | low | transitive | `webpack` | Bundler crypto polyfill weakness |
| `create-ecdh` | low | transitive | `webpack` | Bundler crypto polyfill weakness |
| `crypto-browserify` | low | transitive | `webpack` | Bundler crypto polyfill weakness |
| `elliptic` | low | transitive | `webpack` | Bundler crypto polyfill weakness |
| `node-libs-browser` | low | transitive | `webpack` | Bundler polyfill dependency risk |

The direct parent distribution is: `webpack` 12, `gulp` 5, `gulp-cli` 4,
`jshint` 3, and `webpack-cli` 1. This identifies the smallest useful
remediation workstreams without prescribing upgrades in this pull request.