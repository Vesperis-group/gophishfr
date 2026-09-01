# Frontend third-party inventory

Audit date: 2026-08-19

This inventory covers browser libraries and precompiled assets shipped outside
the normal Yarn dependency graph. Generated files under `static/js/dist` and
`static/css/dist` are build outputs, not additional dependencies. Application
scripts under `static/js/src/app` and the project-specific stylesheets
`main.css`, `dashboard.css`, and `docs.css` are first-party sources.

## Update (Select2 removal, 2026-09-01)

Select2 4.0.13 was removed. Four of the five selects it decorated became plain
native `<select>` elements, and only the campaign group multi-select was given a
replacement component: `tom-select@2.6.2` (Apache-2.0), a framework-independent
library with an official Bootstrap 5 theme and documented accessibility testing.
Tom Select pulls exactly two dependencies, `@orchidjs/sifter@1.1.0` and
`@orchidjs/unicode-variants@1.1.2`, both Apache-2.0.

Select2 decorated five controls across two pages:

| Control | Page | Select2 features actually used | After |
| --- | --- | --- | --- |
| `#template` | `/campaigns` | placeholder, search, sorted options | Native `<select>` |
| `#page` | `/campaigns` | placeholder, search, sorted options | Native `<select>` |
| `#profile` | `/campaigns` | placeholder, search, sorted options | Native `<select>` |
| `#role` | `/users` | none beyond styling; 2 static options | Native `<select>` |
| `#users` | `/campaigns` | multi-select, tags, search, remove control | Tom Select |

The three campaign selects keep a disabled placeholder option whose text is
displayed but whose **value is empty**, so an untouched control submits an empty
name — the same string Select2's blank option produced through `data()`. The
placeholder wording is never submitted. `#role` has no placeholder: it ships two
real options, `admin` and `user`, and always had one of them selected.
Select2's `sorter` was replaced by explicit case-insensitive `localeCompare`
ordering when the options are filled, and by Tom Select's `sortField` for the
group picker. Multi-select therefore keeps click-to-add and a per-tag remove
control: no Ctrl/Cmd+click regression was introduced.

The backend contract is unchanged. The campaign payload has always submitted
option **labels** (`template.name`, `page.name`, `smtp.name`, `groups[].name`),
not values, and the migrated code reproduces that byte for byte.

Establishing that contract required measuring the old widget rather than
reading it. The legacy templates carry a blank `<option></option>` as the first
entry of `#template`, `#page` and `#profile`, and a probe that loaded the
previous `vendor.min.js` and replayed the exact old `setupOptions()` code
showed what it really did:

- the blank option stays selected by default, so an untouched control submitted
  an **empty** name — not the placeholder wording;
- Select2's `sorter` only ordered the rendered dropdown, never the underlying
  `<option>` elements;
- `.select2("val", profile_s2[0])` passed an *object* to `jQuery.val()`, which
  stringified it to `"[object Object]"`, matched no option, and therefore
  preselected nothing; only the `profiles.length === 1` branch ever
  preselected a sending profile;
- a copied campaign whose template no longer exists displayed the missing name
  as a placeholder but still submitted an empty name.

That empty name is load-bearing on the server: `controllers/api/util.go` keys
the default test-template path on `Template.Name == ""`, and
`models/campaign.go` keys `Validate()` on the same emptiness. Submitting the
placeholder wording instead would have silently changed both.

Two intermediate implementations of this change were wrong against that
measured behaviour — one submitted the placeholder wording, another preselected
the first alphabetical sending profile — and both were corrected before commit.
The current code keeps a disabled blank placeholder option whose text is shown
but whose value is empty, so `selectedOptionText()` returns `""` for it, and it
preselects only when a collection holds exactly one entry. The group picker
still drives the real `<select>`, so its option values remain the ones the
server already received, and each group option keeps the
`title="N targets"` tooltip the previous widget set.
`docs/FRONTEND_BROWSER_TESTS.md` coverage was
extended to assert the submitted payload, the empty-name placeholder contract
including the deleted-template case, the single-entry preselection, the
per-tag removal, the empty-search state, the option tooltip, and the absence of
any `.select2-container` node.

Removing Select2 removed a jQuery plugin but not jQuery: 11 first-party
application scripts still contain 433 jQuery usages, driven by DataTables (10
files) and Moment.js formatting (10 files). DataTables is now the **only**
remaining jQuery plugin, which leaves it as the single external blocker for a
future jQuery 4 evaluation. The `#role-select` wrapper div, which existed only
to host Select2's `dropdownParent`, is retained as inert markup.

`@orchidjs/sifter` declares Apache-2.0 in its package metadata but ships no
license file. Rather than fail the licence gate or silently drop the notice,
`scripts/build-frontend.js` gained a `declaredOnlyLicenses` mechanism that emits
an explicit declared-only notice pointing at the reproduced Apache-2.0 text.

Choices.js was considered and rejected: it would not have avoided Apache-2.0
anyway, since it depends on `fuse.js` (Apache-2.0), and it offers no official
Bootstrap 5 theme.

## Update (DateTimePicker removal, 2026-08-20)

The manually vendored Bootstrap DateTimePicker (JS 4.17.37, CSS 4.15.35) was
removed. Campaign scheduling now uses the browser's native
`<input type="datetime-local">` controls, so no replacement dependency, CDN,
theme, or runtime download was added and 112,485 bytes of unmaintained vendored
source left the repository.

The picker was only used on `/campaigns`, for the `launch_date` and
`send_by_date` fields of the campaign modal. Its remaining behaviour was date
and time selection, a "today" shortcut, a prefilled current time on the launch
date, and an empty optional "send emails by" field. No minimum or maximum date,
disabled date, locale, time zone, clear button, or programmatic change was
configured, and no campaign value was ever loaded back into the picker: the
control is only used while creating a campaign.

| Capability | Legacy picker | Used by GophishFR | Required after migration |
| --- | --- | --- | --- |
| Date selection | Yes | Yes | Yes, native |
| Time selection to the minute | Yes | Yes | Yes, native |
| Seconds | Optional | No | No |
| Prefilled current time | Yes, on the launch date | Yes | Yes, first-party |
| Empty optional field | Yes | Yes | Yes |
| Manual keyboard entry | Yes | Yes | Yes, native |
| Minimum or maximum date | Yes | No | No |
| Disabled dates | Yes | No | No |
| Locale | Yes | Default only | Browser locale |
| Explicit time zone | Yes, with `moment-timezone` | No | No |
| Clear and today buttons | Yes | Today only | Browser affordances |
| Programmatic change and callbacks | Yes | No | No |

Candidates were compared before choosing the native controls. Tempus Dominus 6,
the successor written by the same author, is jQuery-free and Bootstrap 5 ready
but its repository states the project is no longer active or supported, so it
was rejected as abandoned. Flatpickr has had no core release since 4.6.13 in
April 2022 and was rejected for the same reason. Air Datepicker 3.6.0 is MIT,
dependency-free and actively maintained, and remains the fallback if a custom
picker is ever required. The native controls were preferred because they cover
the whole capability set actually used, add no dependency, are maintained by the
platform, and provide keyboard and mobile behaviour for free. They are supported
by every browser targeted by Bootstrap 5: Chrome, Edge, Firefox 93 and later,
and Safari 14.1 and later.

The date and time semantics are unchanged end to end. The control holds a local
wall-clock value, `campaigns.js` converts it to the same UTC instant the API has
always received, and the stored and reloaded values are untouched:

```text
user picks local time
  → datetime-local value YYYY-MM-DDTHH:mm, local
  → utcFromLocalDateTimeInput() → UTC RFC 3339, e.g. 2031-06-15T12:30:00Z
  → Go time.Time, stored in UTC
  → campaign lists format it back to local with Moment
```

The one deliberate user-visible change is presentation: the field shows the
browser's locale format instead of the previous `MMMM Do YYYY, h:mm a` string.
Submitting an empty launch date now reports "Please specify a launch date"
instead of posting a value the API rejects; no campaign is created either way.

Moment.js is retained. It is still used by eleven application scripts and the
DataTables sorting plug-in for displaying dates, so the picker was not its only
consumer. jQuery is unchanged at 3.7.1 and still has eleven first-party
consumers.



The manually vendored blueimp jQuery File Upload 5.42.3 and iframe transport
1.8.3 were removed. Group CSV import now uses the browser-native file input,
`FormData`, and `fetch` APIs. No replacement dependency, CDN, telemetry, upload
framework, or runtime download was added.

The existing application behavior required only file selection, multiple
`.csv`/`.txt` files, client-side extension feedback, one authenticated multipart
POST per file, and insertion of the returned targets. The migration deliberately
does not recreate blueimp's implicit document-wide drag/drop, old-browser iframe
transport, progress, preview, cancel, chunking, resumable, or cross-domain
features.

| Capability | Blueimp supports | Used by GophishFR | Required after migration |
| --- | --- | --- | --- |
| Simple file selection | Yes | Yes | Yes, native file input |
| Multipart upload | Yes | Yes, `files[]` to `/api/import/group` | Yes, native `FormData` |
| Multiple files | Yes | Yes, one request per selected file | Yes |
| Drag/drop | Yes, implicitly on `document` | No dedicated UI or application callback | No |
| Progress | Yes | No progress UI or callback | No |
| Validation | Yes | Per-file extension feedback for `.csv` and `.txt` | Yes, first-party; invalid files do not block valid selections |
| Preview | Yes with optional modules | No | No |
| Cancel | Yes | No | No |
| Chunking | Yes | No | No |
| Resumable | Possible with chunking | No | No |
| Cross-domain | Yes, including iframe fallback | No | No |

The backend contract is unchanged: authenticated users with object-modification
permission POST `multipart/form-data` to `/api/import/group`, using `files[]`,
and receive a JSON array of target records. The browser continues to send the
API key in the `Authorization: Bearer` header. The endpoint streams CSV content
without persisting the upload or using its filename, so filename path traversal
and permanent upload storage are not applicable.

Browser extension and `accept` checks are usability controls, not a security
boundary. The server remains authoritative for CSV parsing and email-address
validation. A separate backend-hardening change should add an explicit POST-only
route, request and record-count limits, multipart part-count limits, and a
documented server-side content policy; the current endpoint has none of those
limits. That independent work is intentionally not hidden in this frontend
replacement.

Removing blueimp eliminated the only Widget Factory consumer. The exact
`jquery-ui@1.14.2` dependency, its lockfile entry, bundled `ui/widget.js`, and
generated license notice were therefore removed. jQuery UI is no longer
distributed. jQuery 3.7.1 remains unchanged for DataTables, Select2 and
first-party code; 11 first-party application scripts still
contain jQuery usage.

## Update (jQuery modernization, 2026-08-19)

- The vendored jQuery 1.10.2 runtime was replaced by the exact Yarn dependency
  `jquery@3.7.1`. Its direct version satisfies the DataTables range, so Yarn
  resolves one package and the browser exposes one
  `window.jQuery === window.$` instance.
- The vendored jQuery UI Widget Factory 1.11.1 was initially replaced by
  `jquery-ui@1.14.2` for blueimp File Upload. That intermediate dependency was
  removed with blueimp; no jQuery UI code is now distributed.
- First-party jqXHR callbacks use `.done()`/`.fail()`/`.always()`.
  DateTimePicker used `.length` instead of removed `.size()` and inserted its
  detached widget before showing it under jQuery 3, until the picker was
  replaced by native controls. The former blueimp
  `.pipe()` compatibility patch disappeared with the plugin.
- jQuery Migrate 3.6.0 was evaluated but not added or shipped. Static analysis
  identified the incompatibilities directly, and the production browser suite
  completes with no JavaScript exception or console error.
- Retire.js 5.4.3 progressed from 9 findings before migration, to 4 after the
  jQuery core replacement, to 0 after Widget Factory 1.14.2.

## Update (Bootstrap 5 migration, 2026-08-19)

Bootstrap and the DataTables Bootstrap integration moved from manually
vendored/inventoried components to exact Yarn dependencies as part of
`refactor/migrate-bootstrap5`:

- Bootstrap 3.0.2 JS (`static/js/src/vendor/bootstrap.min.js`) and Bootstrap
  3.3.7 CSS (`static/css/bootstrap.min.css`) were deleted, along with the
  Glyphicon font files under `static/font/`. Bootstrap 5.3.8 is now an exact
  Yarn dependency; `bootstrap.bundle.min.js` (which includes Popper) and
  `bootstrap.min.css` are built from `node_modules/bootstrap`, and its full
  license text is emitted in `static/js/dist/vendor.min.js.LICENSE.txt`.
  Bootstrap's exact `@popperjs/core@2.11.8` peer dependency is also managed by
  Yarn and included in the generated notices.
- `datatables.net-bs` 1.13.11 was replaced by `datatables.net-bs5` 1.13.11 in
  `package.json`.
- `static/css/select2-bootstrap.min.css` (Select2 Bootstrap Theme, a Bootstrap
  3 theme) was deleted; Select2 now uses its bundled `default` theme.
- `static/css/flat-ui.css` was deleted from the repository. Its historical
  attribution is preserved in NOTICE. `static/css/gophishfr-theme.css` is the
  new first-party Bootstrap 5 theme (system font stack, no external CDN).
- Bootstrap does NOT use its optional jQuery bridge (`data-bs-no-jquery` is set
  on `<body>`). All Bootstrap lifecycle events are handled via native
  `addEventListener`. jQuery remains for DataTables, Select2,
  and first-party application code.
- Bootstrap DateTimePicker's vendored source (`bootstrap-datetime.js`) was
  locally modified to: (1) replace Collapse jQuery calls with native
  `bootstrap.Collapse.getOrCreateInstance()` API, and (2) change default icon
  classes from Glyphicon to Font Awesome. That plugin was later removed.
- Google Fonts external stylesheet links were removed. Typography uses a
  system-local font stack defined in `gophishfr-theme.css` — no CDN or
  external runtime asset is loaded.

The rest of this document, written before the migration, describes the
Bootstrap 3/Flat UI-era inventory for historical reference; entries below are
annotated where the migration superseded them.

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
| jQuery | 1.10.2 — **superseded**: vendored copy deleted, replaced by exact Yarn dependency `jquery@3.7.1` (see Yarn-managed table below) | `static/js/src/vendor/jquery.js` (deleted) | Formerly used globally; now supplied from `node_modules/jquery/dist/jquery.js` as the single runtime instance | [jquery/jquery](https://github.com/jquery/jquery), MIT | Migrated to 3.7.1; CVE-2015-9251, CVE-2019-11358, CVE-2020-11022, CVE-2020-11023 remediated |
| Bootstrap | JS 3.0.2; CSS and Glyphicons 3.3.7, file banners — **superseded**: removed, replaced by the exact Yarn dependency `bootstrap@5.3.8` (see update note above) | `bootstrap.min.js`, `bootstrap.min.css`, `static/font/glyphicons-*` (all deleted) | Formerly used for layout, modal, tabs, tooltip; minified-only | [twbs/bootstrap](https://github.com/twbs/bootstrap); JS Apache-2.0, CSS MIT, Glyphicons' Bootstrap-specific grant | Migrated; see `docs/BOOTSTRAP5_MIGRATION_BASELINE.md` |
| normalize.css | 3.0.3, embedded banner | Embedded in `bootstrap.min.css` | `USED` as Bootstrap reset; minified-only | [necolas/normalize.css](https://github.com/necolas/normalize.css), MIT | `CLEAN`; keep with Bootstrap CSS |
| D3 | 3.5.3, embedded `version` and exact npm package hash | `static/js/src/vendor/d3.min.js` | `LEGACY_BUT_REQUIRED` by Datamaps; minified-only | [d3/d3](https://github.com/d3/d3), BSD-3-Clause | `CLEAN`, old release line; migrate with Datamaps |
| TopoJSON | 1.6.9, embedded `version` | `static/js/src/vendor/topojson.min.js` | `LEGACY_BUT_REQUIRED` by Datamaps; minified-only | [topojson/topojson](https://github.com/topojson/topojson), BSD-3-Clause | `CLEAN`, old release line; migrate with Datamaps |
| Datamaps | `UNKNOWN`; hash does not match official npm 0.3.6 through 0.5.10 world bundles | `static/js/src/vendor/datamaps.min.js` | `USED` by the campaign-results map; minified-only | Historical import `a78e92a`; [markmarkoh/datamaps](https://github.com/markmarkoh/datamaps), MIT | `UNKNOWN_VERSION`, archived upstream; replace with a maintained map implementation |
| DataTables datetime-moment plug-in | `UNKNOWN`; no version marker | `static/js/src/vendor/datetime-moment.js` | `USED` for date sorting; readable source retained | [DataTables plug-in](https://datatables.net/plug-ins/sorting/datetime-moment), MIT | `UNKNOWN_VERSION`, deprecated upstream; replace with DataTables' current date renderer |
| jQuery UI Widget Factory | 1.11.1, later Yarn-managed 1.14.2 — **removed** | Former vendored file and package-managed `ui/widget.js` are deleted | Its only consumer was blueimp File Upload | [jquery/jquery-ui](https://github.com/jquery/jquery-ui), MIT | `REMOVED`; jQuery UI is no longer distributed |
| blueimp jQuery File Upload | 5.42.3; iframe transport 1.8.3 — **removed** | `jquery.fileupload.js`, `jquery.iframe-transport.js` (deleted) | Formerly used only by group CSV import; replaced with first-party native browser APIs | [blueimp/jQuery-File-Upload](https://github.com/blueimp/jQuery-File-Upload), MIT | `REMOVED`; CVE-2018-9206 concerned upstream server handlers, which were never shipped |
| SweetAlert2 | 8.17.1, exact npm package hash | `sweetalert2.min.js`, `sweetalert2.min.css` | `USED` for confirmations and status dialogs; minified-only | [sweetalert2/sweetalert2](https://github.com/sweetalert2/sweetalert2), MIT | `CLEAN`, old major; preserve pending UI-stack migration |
| Bootstrap DateTimePicker | JS 4.17.37; CSS 4.15.35 — **removed** | `bootstrap-datetime.js`, `bootstrap-datetime.css` (deleted) | Formerly used for campaign scheduling; replaced by native `<input type="datetime-local">` controls | [Eonasdan/bootstrap-datetimepicker](https://github.com/Eonasdan/bootstrap-datetimepicker), MIT | `REMOVED`; it was `UNMAINTAINED` and its successor is discontinued, so no third-party picker is distributed |
| Select2 Bootstrap Theme | 0.1.0-beta.9, banner — **superseded**: removed; Select2 now uses its bundled `default` theme, restyled in `static/css/gophishfr-theme.css` | `static/css/select2-bootstrap.min.css` (deleted) | Formerly used for Select2 presentation; minified-only CSS | [select2-bootstrap-theme](https://github.com/select2/select2-bootstrap-theme), MIT | Migrated; see `docs/BOOTSTRAP5_MIGRATION_BASELINE.md` |
| core-js browser bundle | 2.4.1, source banner | `static/js/src/vendor/core.min.js` | `LEGACY_BUT_REQUIRED` Promise polyfill; minified-only | [zloirock/core-js](https://github.com/zloirock/core-js), MIT | `UNMAINTAINED`; no applicable runtime advisory found; reassess with browser policy |
| Font Awesome | 4.7.0, CSS banner | `font-awesome.min.css`, `static/font/fontawesome-*` | `USED` across the admin UI; minified CSS and font binaries | [FortAwesome/Font-Awesome](https://github.com/FortAwesome/Font-Awesome), CSS MIT and fonts SIL OFL 1.1 | `CLEAN`, old release line; migrate with UI stack |
| Flat UI-derived CSS | 2.1.3 metadata at exact upstream snapshot `097631e`; imported with 14 insertions and 122 deletions, then modified — **removed** from repository during Bootstrap 5 migration | `static/css/flat-ui.css` (deleted) | No longer distributed; historical attribution in NOTICE | [Designmodo Flat UI Free at `097631e`](https://github.com/designmodo/Flat-UI/tree/097631e59b9950312052123a65cbcbaf97dc740a), CC BY 3.0 and MIT upstream terms | Removed |
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
| jQuery | 3.7.1 | Single global runtime instance for DataTables and first-party code; the exact direct dependency naturally satisfies all transitive ranges | MIT | `CLEAN`; remediates CVE-2015-9251, CVE-2019-11358, CVE-2020-11022, CVE-2020-11023 from 1.10.2 |
| Chart.js / `@kurkle/color` | 4.5.1 / 0.3.4 | Dashboard and campaign charts | MIT | `CLEAN` |
| chartjs-plugin-zoom / Hammer.JS | 2.2.0 / 2.0.8 | Timeline pan and zoom | MIT | `CLEAN` |
| Bootstrap / Popper | 5.3.8 / 2.11.8 | Global layout, navbar, modal, tabs, dropdown, tooltip | MIT | `CLEAN`; exact Yarn dependencies replacing the manually vendored Bootstrap 3 JS/CSS above |
| DataTables / Bootstrap 5 integration (`datatables.net-bs5`) | 1.13.11 | Admin tables | MIT | `CLEAN`; its jQuery range resolves to the single direct `jquery@3.7.1` installation |
| Moment.js | 2.30.1 | Date parsing and formatting | MIT | `CLEAN`, maintenance mode |
| Papa Parse | 5.6.0 | CSV import and export | MIT | `CLEAN` |
| Tom Select / `@orchidjs/sifter` / `@orchidjs/unicode-variants` | 2.6.2 / 1.1.0 / 1.1.2 | Campaign group multi-select only; framework-independent, no jQuery | Apache-2.0 | `CLEAN`; replaces Select2 4.0.13 |
| UAParser.js | 0.7.41 | Recipient-controlled user-agent parsing | MIT | `CLEAN` |
| zxcvbn | 4.4.2 | Password-strength feedback | MIT | `CLEAN`, old release |
| CodeMirror / Lezer | CodeMirror packages 6.x; Lezer packages 1.x, exact versions in `yarn.lock` | Canonical full-document HTML source editing, syntax highlighting, search, keyboard commands, and GophishFR placeholder completion | MIT | `CLEAN`; self-hosted Webpack bundle, no remote service or license key |

Yarn audit reports zero advisories across 115 resolved dependencies, and
Retire.js 5.4.3 reports zero findings against the generated bundles. A
temporary npm resolution, created outside the worktree without retaining a
`package-lock.json`, reports zero advisories across 108 dependencies. GitHub
reports zero open Dependabot alerts.

## Changes made by this audit

| Component | Before | After | Classification | Reason |
| --- | --- | --- | --- | --- |
| Moment.js | Vendored 2.10.3 | Yarn 2.30.1 | `SAFE_UPDATE`, `MOVE_TO_PACKAGE_MANAGER` | Same major API; removes browser-relevant ReDoS findings |
| Papa Parse | Vendored 5.2.0 | Yarn 5.6.0 | `SAFE_UPDATE`, `MOVE_TO_PACKAGE_MANAGER` | Same major API; establishes lockfile provenance |
| DataTables | Vendored 1.10.11 | Yarn 1.13.11 | `SAFE_UPDATE`, `MOVE_TO_PACKAGE_MANAGER` | Last 1.x API line; removes prototype-pollution and XSS findings |
| Select2 | Vendored 4.0.3 | Yarn 4.0.13 — **superseded**: removed entirely; see the Select2 removal update | `SAFE_UPDATE`, `MOVE_TO_PACKAGE_MANAGER` | Narrow 4.0 update; removes CVE-2016-10744 without 4.1 behavior changes |
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

Before the Bootstrap migration, Retire.js 5.4.3 reported 16 source signatures:
14 medium and 2 low across Bootstrap, jQuery, and jQuery UI. Bootstrap removal
reduced that baseline to 9 findings (8 medium, 1 low). Replacing jQuery core
reduced it to the 4 Widget Factory false-component matches, and replacing
Widget Factory 1.11.1 with 1.14.2 reduced the final runtime-source scan to
zero findings.

### CKEditor 4.11.1

The vulnerable, end-of-life CKEditor distribution was removed. CodeMirror 6
now edits the canonical HTML text without parsing or normalizing it. A derived,
read-only iframe provides visual preview with scripts, same-origin access,
forms, navigation, and external resources disabled. The candidate and security
analysis is documented in `docs/HTML_EDITOR_MIGRATION.md`.

### jQuery 1.10.2 → 3.7.1 (migrated)

Applicable findings CVE-2015-9251, CVE-2019-11358, CVE-2020-11022, and
CVE-2020-11023 are all remediated by upgrading to jQuery 3.7.1 (the
compatibility bridge release). First-party `.success()`/`.error()`/`.complete()`
calls replaced with `.done()`/`.fail()`/`.always()`. Vendored DateTimePicker
`.size()` was replaced with `.length`, and its detached widget was inserted
before `.show()` so jQuery 3 could override Bootstrap's hidden dropdown state,
until that picker was replaced by native controls.
Vendored blueimp File Upload `.pipe()` was temporarily replaced with `.then()`
before the plugin was removed. DataTables 1.13.11 remains
confirmed compatible under jQuery 3.7.1, as did Select2 4.0.13 and
DateTimePicker 4.17.37 until both were replaced.

jQuery Migrate 3.6.0 was evaluated as a temporary diagnostic option but was
not added or loaded: the static audit identified the removed APIs directly,
and production-representative Playwright coverage runs with zero JavaScript
exception or console error. No Migrate code is present in the runtime,
manifest, lockfile, or test dependencies.

**JQUERY4_BLOCKED reasons** (preliminary):
- DataTables 1.x: uses `$.camelCase` and other internals removed in 4.x
- first-party application scripts still use jQuery for Ajax, DOM, and events
- Resolution: remain on 3.7.1 until plugin majors are upgraded or replaced

The DateTimePicker blocker was removed with the picker itself: campaign
scheduling no longer runs any jQuery plugin. The Select2 blocker
(`$.expr[':']`, removed in jQuery 4.x) was removed with Select2 itself:
DataTables is now the only remaining jQuery plugin.

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

The post-Select2 frontend build contains 19 generated files and
2,860,106 bytes, 3,466 bytes larger than the previous build of 2,856,640
bytes. That growth is entirely license text: `vendor.min.js.LICENSE.txt` grew
from 13,682 to 35,477 bytes (+21,795) because Tom Select and its two
`@orchidjs` dependencies are Apache-2.0 and the full license text is
reproduced. The executed code shrank by 18,329 bytes: the vendor bundle
decreased from 1,045,486 to 1,026,331 bytes (-19,155, Tom Select being smaller
than Select2) and `users.min.js` from 5,383 to 5,036 bytes (-347), against
`campaigns.min.js` growing from 8,612 to 9,049 bytes (+437) for the native
select helpers and the stylesheet from 330,625 to 331,361 bytes (+736).
Two independent immutable
installations and clean builds produced the
same per-file hashes, left `yarn.lock` unchanged, and produced this aggregate
SHA-256 manifest:

```text
742ffe360924756f13a378b7867f4795c94faf79ec1b2f7726b1d389029ea162
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
