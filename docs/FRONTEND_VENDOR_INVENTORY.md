# Frontend third-party inventory

Audit date: 2026-08-19

This inventory covers browser libraries and precompiled assets shipped outside
the normal Yarn dependency graph. Generated files under `static/js/dist` and
`static/css/dist` are build outputs, not additional dependencies. Application
scripts under `static/js/src/app` and the project-specific stylesheets
`main.css`, `dashboard.css`, and `docs.css` are first-party sources.

## Method and status definitions

The audit combined file banners and embedded constants, exact hashes against
published packages, repository history, build source lists, runtime references,
Yarn audit, Dependabot, and Retire.js 5.4.3. Published package hashes were
compared without changing the repository dependency graph. A version is
`UNKNOWN` when those sources do not establish it with sufficient confidence.

- `CLEAN`: no applicable known advisory was found for the identified version.
- `VULNERABLE`: at least one applicable known advisory affects the component.
- `UNKNOWN_VERSION`: the exact version cannot be established reliably.
- `UNMAINTAINED`: the shipped release line no longer receives normal upstream
  security maintenance.
- `NOT_APPLICABLE`: a scanner matched a package advisory, but the affected
  module or execution context is not shipped.

`CLEAN` is evidence from this audit, not a guarantee that no vulnerability
exists.

## Manually vendored components

| Component | Version and evidence | Distribution | Usage and retained form | Provenance and license | Security and action |
| --- | --- | --- | --- | --- | --- |
| jQuery | 1.10.2, source banner | `static/js/src/vendor/jquery.js` | `USED` globally; minified-only historical copy | [jquery/jquery](https://github.com/jquery/jquery), MIT banner retained | `VULNERABLE`, `UNMAINTAINED`; migrate the legacy plug-in stack to jQuery 3.5+ |
| Bootstrap | JS 3.0.2; CSS and Glyphicons 3.3.7, file banners | `bootstrap.min.js`, `bootstrap.min.css`, `static/font/glyphicons-*` | `USED` for layout, modal, tabs, tooltip; minified-only | [twbs/bootstrap](https://github.com/twbs/bootstrap); JS Apache-2.0, CSS MIT, Glyphicons' Bootstrap-specific grant | `VULNERABLE`, `UNMAINTAINED`; migrate the UI stack |
| normalize.css | 3.0.3, embedded banner | Embedded in `bootstrap.min.css` | `USED` as Bootstrap reset; minified-only | [necolas/normalize.css](https://github.com/necolas/normalize.css), MIT | `CLEAN`; keep with Bootstrap CSS |
| D3 | 3.5.3, embedded `version` and exact npm package hash | `static/js/src/vendor/d3.min.js` | `LEGACY_BUT_REQUIRED` by Datamaps; minified-only | [d3/d3](https://github.com/d3/d3), BSD-3-Clause | `CLEAN`, old release line; migrate with Datamaps |
| TopoJSON | 1.6.9, embedded `version` | `static/js/src/vendor/topojson.min.js` | `LEGACY_BUT_REQUIRED` by Datamaps; minified-only | [topojson/topojson](https://github.com/topojson/topojson), BSD-3-Clause | `CLEAN`, old release line; migrate with Datamaps |
| Datamaps | `UNKNOWN`; hash does not match official npm 0.3.6 through 0.5.10 world bundles | `static/js/src/vendor/datamaps.min.js` | `USED` by the campaign-results map; minified-only | Historical import `a78e92a`; [markmarkoh/datamaps](https://github.com/markmarkoh/datamaps), MIT | `UNKNOWN_VERSION`, archived upstream; replace with a maintained map implementation |
| DataTables datetime-moment plug-in | `UNKNOWN`; no version marker | `static/js/src/vendor/datetime-moment.js` | `USED` for date sorting; readable source retained | [DataTables plug-in](https://datatables.net/plug-ins/sorting/datetime-moment), MIT | `UNKNOWN_VERSION`, deprecated upstream; replace with DataTables' current date renderer |
| jQuery UI Widget Factory | 1.11.1, source banner | `static/js/src/vendor/jquery.ui.widget.js` | `LEGACY_BUT_REQUIRED` by blueimp File Upload; readable source retained | [jquery/jquery-ui](https://github.com/jquery/jquery-ui), MIT | Retire.js matches four advisories for absent Datepicker, Position, and Checkboxradio modules: `NOT_APPLICABLE`; migrate with file upload |
| blueimp jQuery File Upload | 5.42.3; iframe transport 1.8.3, source banners | `jquery.fileupload.js`, `jquery.iframe-transport.js` | `USED` by group CSV import; readable source retained | [blueimp/jQuery-File-Upload](https://github.com/blueimp/jQuery-File-Upload), MIT | Browser modules have no applicable advisory; CVE-2018-9206 affected upstream server handlers, which are not shipped; migrate with jQuery |
| SweetAlert2 | 8.17.1, exact npm package hash | `sweetalert2.min.js`, `sweetalert2.min.css` | `USED` for confirmations and status dialogs; minified-only | [sweetalert2/sweetalert2](https://github.com/sweetalert2/sweetalert2), MIT | `CLEAN`, old major; preserve pending UI-stack migration |
| Bootstrap DateTimePicker | JS 4.17.37; CSS 4.15.35, banners | `bootstrap-datetime.js`, `bootstrap-datetime.css` | `USED` for campaign scheduling; readable JS, CSS source | [Eonasdan/bootstrap-datetimepicker](https://github.com/Eonasdan/bootstrap-datetimepicker), MIT | `UNMAINTAINED`; mismatched patch versions, no applicable advisory found; migrate with Bootstrap |
| Select2 Bootstrap Theme | 0.1.0-beta.9, banner | `static/css/select2-bootstrap.min.css` | `USED` for Select2 presentation; minified-only CSS | [select2-bootstrap-theme](https://github.com/select2/select2-bootstrap-theme), MIT | `CLEAN`, archived upstream; migrate with Bootstrap |
| core-js browser bundle | 2.4.1, source banner | `static/js/src/vendor/core.min.js` | `LEGACY_BUT_REQUIRED` Promise polyfill; minified-only | [zloirock/core-js](https://github.com/zloirock/core-js), MIT | `UNMAINTAINED`; no applicable runtime advisory found; reassess with browser policy |
| Font Awesome | 4.7.0, CSS banner | `font-awesome.min.css`, `static/font/fontawesome-*` | `USED` across the admin UI; minified CSS and font binaries | [FortAwesome/Font-Awesome](https://github.com/FortAwesome/Font-Awesome), CSS MIT and fonts SIL OFL 1.1 | `CLEAN`, old release line; migrate with UI stack |
| Flat UI-derived CSS | 2.1.3 metadata at exact upstream snapshot `097631e`; imported with 14 insertions and 122 deletions, then modified | `static/css/flat-ui.css` | `USED` for global Bootstrap 3 theme overrides; readable derived source retained | [Designmodo Flat UI Free at `097631e`](https://github.com/designmodo/Flat-UI/tree/097631e59b9950312052123a65cbcbaf97dc740a), CC BY 3.0 and MIT upstream terms; Gophish/GophishFR modifications recorded in history | `LOCALLY_MODIFIED`, `HIGH` provenance confidence; no known advisory; replace with explicit design tokens during Bootstrap 5 migration |
| Awesome Bootstrap Checkbox-derived CSS | 0.3.6-derived; eleven-line diff from the published 0.3.6 CSS | `static/css/checkbox.css` | `USED` for checkbox styling; readable modified source retained | [flatlogic/awesome-bootstrap-checkbox](https://github.com/flatlogic/awesome-bootstrap-checkbox), Copyright 2014 flatlogic.com, MIT | `CLEAN`; retain attribution and migrate with UI stack |

No tracked JavaScript or CSS source maps are present. References to absent maps
inside minified upstream files are development metadata and are not loaded
during normal page execution.

## Yarn-managed browser components

Yarn Classic 1.22.22 and `yarn.lock` are the source of truth. All direct
versions are exact. The build reads these files from `node_modules`, bundles
them in a fixed order, and emits their complete license texts in
`static/js/dist/vendor.min.js.LICENSE.txt`.

| Component | Version | Usage | License | Audit status |
| --- | --- | --- | --- | --- |
| Chart.js / `@kurkle/color` | 4.5.1 / 0.3.4 | Dashboard and campaign charts | MIT | `CLEAN` |
| chartjs-plugin-zoom / Hammer.JS | 2.2.0 / 2.0.8 | Timeline pan and zoom | MIT | `CLEAN` |
| DataTables / Bootstrap integration | 1.13.11 | Admin tables | MIT | `CLEAN`; `jquery@4.0.0` is a lockfile-only transitive dependency and is not bundled because browser mode uses the existing global jQuery |
| Moment.js | 2.30.1 | Date parsing and formatting | MIT | `CLEAN`, maintenance mode |
| Papa Parse | 5.6.0 | CSV import and export | MIT | `CLEAN` |
| Select2 | 4.0.13 | Campaign and role selectors | MIT | `CLEAN` |
| UAParser.js | 0.7.41 | Recipient-controlled user-agent parsing | MIT | `CLEAN` |
| zxcvbn | 4.4.2 | Password-strength feedback | MIT | `CLEAN`, old release |
| CodeMirror / Lezer | CodeMirror packages 6.x; Lezer packages 1.x, exact versions in `yarn.lock` | Canonical full-document HTML source editing, syntax highlighting, search, keyboard commands, and GophishFR placeholder completion | MIT | `CLEAN`; self-hosted Webpack bundle, no remote service or license key |

Yarn audit reports zero advisories across 110 resolved dependencies. A temporary
npm resolution, created outside the worktree without retaining a
`package-lock.json`, also reports zero advisories. GitHub reports zero open
Dependabot alerts.

## Changes made by this audit

| Component | Before | After | Classification | Reason |
| --- | --- | --- | --- | --- |
| Moment.js | Vendored 2.10.3 | Yarn 2.30.1 | `SAFE_UPDATE`, `MOVE_TO_PACKAGE_MANAGER` | Same major API; removes browser-relevant ReDoS findings |
| Papa Parse | Vendored 5.2.0 | Yarn 5.6.0 | `SAFE_UPDATE`, `MOVE_TO_PACKAGE_MANAGER` | Same major API; establishes lockfile provenance |
| DataTables | Vendored 1.10.11 | Yarn 1.13.11 | `SAFE_UPDATE`, `MOVE_TO_PACKAGE_MANAGER` | Last 1.x API line; removes prototype-pollution and XSS findings |
| Select2 | Vendored 4.0.3 | Yarn 4.0.13 | `SAFE_UPDATE`, `MOVE_TO_PACKAGE_MANAGER` | Narrow 4.0 update; removes CVE-2016-10744 without 4.1 behavior changes |
| UAParser.js | Vendored 0.7.18 | Yarn 0.7.41 | `SAFE_UPDATE`, `MOVE_TO_PACKAGE_MANAGER` | Same 0.7 API; removes three ReDoS findings on recipient-controlled input |
| Chartist | Vendored JS 0.9.2, CSS, and dead first-party selectors | Removed | `SAFE_REMOVE` | No runtime reference; Chart.js had already replaced the chart stack |
| DataTables standalone CSS | Unreferenced source files | Removed | `SAFE_REMOVE` | Bootstrap integration CSS is the only stylesheet built |
| Empty vendor script | Zero-byte `sending_profiles.js` | Removed | `SAFE_REMOVE` | No reference; the first-party application script is separate |
| CKEditor | Vendored 4.11.1 custom build | Removed and replaced by exact-pinned CodeMirror 6 packages | `SECURITY_REPLACEMENT` | Removes an EOL editor with applicable XSS advisories while preserving the full-document source contract |
| Flat UI icon definitions | Embedded `@font-face`, `data-icon`, and concrete `fui-*` glyph rules without any distributed font | Removed | `SAFE_REMOVE` | No matching template or script reference; icon assets were absent from the original import and Font Awesome is the active icon system |
| Bootstrap Switch | Embedded CSS 1.3 without JavaScript, markup, or mask image | Removed | `SAFE_REMOVE` | No matching template or script reference; the absent image was never imported |

Highcharts 5.0.14 was removed and replaced by Chart.js in PR #18 before this
audit was reconciled. It remains documentation-only: no Highcharts source,
bundle, or runtime reference is distributed.

## Known findings and deferred migrations

After the CKEditor removal, Retire.js 5.4.3 reports 16 source signatures: 14
medium and 2 low. They occur in Bootstrap, jQuery, and jQuery UI Widget Factory.
The four jQuery UI findings are false component matches because the affected
widgets are absent. Retire.js reports no critical or high-severity finding, but
the remaining legacy UI stack still makes the following migrations security
priorities. Scanning source and generated JavaScript reports 32 signatures;
the 16 additional matches are duplicates for the same Bootstrap, jQuery, and
jQuery UI code inside `vendor.min.js`, not new components or advisories.

### CKEditor 4.11.1

The vulnerable, end-of-life CKEditor distribution was removed. CodeMirror 6
now edits the canonical HTML text without parsing or normalizing it. A derived,
read-only iframe provides visual preview with scripts, same-origin access,
forms, navigation, and external resources disabled. The candidate and security
analysis is documented in `docs/HTML_EDITOR_MIGRATION.md`.

### jQuery 1.10.2

Applicable findings include CVE-2015-9251, CVE-2019-11358,
CVE-2020-11022, and CVE-2020-11023. The latter two are fixed in jQuery 3.5.0.
The application and all legacy plug-ins require compatibility testing across
that major-version jump.

### Bootstrap JS 3.0.2

Applicable XSS findings include CVE-2016-10735, CVE-2018-14040,
CVE-2018-14041, CVE-2018-14042, CVE-2018-20676, CVE-2018-20677,
CVE-2019-8331, and CVE-2024-6485. Bootstrap 3.4.1 fixes the older advisories
but remains end-of-life and does not contain an upstream open-source fix for
CVE-2024-6485. The durable remediation is a supported Bootstrap migration.

## License and provenance limits

- Datamaps remains `UNKNOWN_VERSION`; its hash does not match any published
  npm world bundle from 0.3.6 through 0.5.10. The historical file is clearly
  Datamaps and retains the upstream MIT obligation, but is not reproducible
  from a known release artifact.
- The Flat UI stylesheet is a long-lived derivative rather than an unchanged
  tagged artifact. Its exact source is the post-2.1.3 upstream commit
  `097631e59b9950312052123a65cbcbaf97dc740a`; the upstream metadata applies
  CC BY 3.0 and MIT to the distribution without a per-file split.
- `checkbox.css` is now identified as a modified Awesome Bootstrap Checkbox
  0.3.6 file under MIT; its local changes affect eleven diff lines.
- Package-managed components' complete notices are generated beside their
  bundles. CodeMirror and Lezer notices are emitted as
  `static/js/dist/app/html_editor.min.js.LICENSE.txt`.

## Reproducibility

The current frontend build contains 19 generated files and 2,858,432 bytes.
Two independent immutable installations and clean builds produced the same
per-file hashes, left `yarn.lock` unchanged, and produced this aggregate
SHA-256 manifest:

```text
8d79f5299d209269fd63f9e703ad988c70bb8bc1f5fc51818b4874828b716140
```

Webpack reports performance-budget warnings for the approximately 532 KiB
CodeMirror bundle and the pre-existing approximately 801 KiB password-strength
bundle. Both are self-hosted, deterministic build outputs; splitting them is a
performance concern, not part of this security replacement.

## Future security ratchet

Use one pinned Retire.js version for file-signature/advisory detection, backed
by this reviewed inventory:

1. Scan source and generated JavaScript in CI and compare advisory identifiers
   with an explicit reviewed baseline.
2. Fail when a new file appears under the vendored source directories without
   an inventory entry, or when an inventoried file changes without a version,
   provenance, and license review.
3. Keep Yarn audit and Dependabot as the authority for managed packages.

OSV alone does not reliably identify anonymous minified files, and a
package-manager SBOM cannot represent them without additional metadata. The
proposed follow-up is `security/frontend-sca-ratchet`.
