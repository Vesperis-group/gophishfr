# Bootstrap 5 migration baseline

Audit date: 2026-08-19

This document records the COMPLETE Bootstrap 5 migration status. All runtime
Bootstrap 3, Flat UI, and Glyphicon dependencies have been removed and replaced.

## Migration status (2026-08-19)

The Bootstrap 5 migration described as future work below is complete:

- Bootstrap 3.3.7 CSS and Bootstrap 3.0.2 JS were removed. Bootstrap 5.3.8 and
  its `@popperjs/core` 2.11.8 peer are exact Yarn dependencies, built from
  `node_modules/bootstrap` via
  `scripts/build-frontend.js` (`bootstrap.min.css` and
  `bootstrap.bundle.min.js`, which includes Popper).
- `static/css/flat-ui.css` was deleted from the repository. Historical
  attribution is preserved in NOTICE. `static/css/gophishfr-theme.css`
  is the new first-party stylesheet that replaces its runtime role: global
  typography (system font stack), link colors, the dark navbar palette, the
  primary button/badge accent color, form focus styling, and Select2 accents.
- `static/css/select2-bootstrap.min.css` (a Bootstrap 3 Select2 theme) was
  removed; Select2 now uses its bundled `default` theme, restyled in
  `gophishfr-theme.css`.
- DataTables' Bootstrap integration moved from `datatables.net-bs` 1.13.11 to
  `datatables.net-bs5` 1.13.11.
- Glyphicon font files were removed. Bootstrap DateTimePicker (kept, still
  jQuery-based) has its vendored default icons changed from Glyphicon to Font
  Awesome, and every initialization supplies explicit `icons: faIcons`.
- Bootstrap DateTimePicker's Collapse calls were replaced with native
  `bootstrap.Collapse.getOrCreateInstance()` API (no jQuery bridge dependency).
- All templates were migrated to Bootstrap 5 markup: `data-bs-*` attributes,
  `navbar-expand-lg`/`navbar-dark` navbar structure (with a first-party
  `navbar-gophishfr` color class replacing the old `navbar-inverse` override),
  `btn-close`, restructured modal headers, `nav-item`/`nav-link` tabs,
  `offset-*` grid classes, `float-end`/`float-start`, `btn-secondary`,
  `form-label`/`form-text`/`mb-3`, `table-sm`, and `badge`/`bg-*` (or
  `text-bg-*` for colored status pills) in place of `label`/`label-*`.
- Bootstrap does NOT use its optional jQuery plugin bridge. `data-bs-no-jquery`
  is set on all `<body>` elements, preventing Bootstrap from registering
  `$.fn.modal`, `$.fn.tooltip`, etc. All Bootstrap lifecycle event listeners
  (`hidden.bs.modal`, `shown.bs.modal`, `hide.bs.modal`) use native
  `addEventListener` (centralized modal-stack logic in `gophish.js`, per-page
  dismiss handlers in each page script).
- Google Fonts external stylesheet links were removed. Typography uses a
  system-local font stack — no CDN or external runtime asset is loaded.
- `tests/browser/frontend-smoke.spec.ts` asserts the jQuery bridge is absent
  (`$.fn.modal` is not a function), native Bootstrap API works, and blocks
  any unexpected external request (no fonts.googleapis.com special-case).

## Current stack

The generated stylesheet (`static/css/dist/gophish.css`) is built in this order:

1. Bootstrap 5.3.8 CSS (from `node_modules/bootstrap`);
2. first-party GophishFR styles (`main.css`, `dashboard.css`, `gophishfr-theme.css`);
3. DataTables Bootstrap 5 integration CSS;
4. Font Awesome, Bootstrap DateTimePicker CSS, checkbox.css, SweetAlert2, Select2.

The browser vendor bundle (`static/js/dist/vendor.min.js`) includes jQuery
1.10.2, Bootstrap 5.3.8 bundle (with Popper), and legacy jQuery plugins
(generated in that order by `scripts/build-frontend.js`).
Bootstrap's optional jQuery bridge is disabled via `data-bs-no-jquery` on
`<body>`. All Bootstrap lifecycle event listeners use native `addEventListener`.
No external CDN, fonts, or runtime assets are loaded. Typography uses a
system-local font stack.

**Remaining jQuery/jQuery UI debt:** jQuery 1.10.2 (vulnerable, unmaintained)
remains required by DataTables, Select2, DateTimePicker, blueimp File Upload,
SweetAlert2, and first-party DOM manipulation (`$(...)`). jQuery UI Widget
Factory 1.11.1 is retained for blueimp File Upload.

**Flat UI:** Deleted from the repository. Historical attribution in NOTICE.

## Build and size result

| Artifact | Bootstrap 3 baseline | Bootstrap 5 result | Delta |
| --- | ---: | ---: | ---: |
| Generated files | 19 | 19 | 0 |
| All generated frontend assets | 2,858,432 bytes | 2,933,334 bytes | +74,902 bytes |
| `static/css/dist/gophish.css` | 318,028 bytes | 338,328 bytes | +20,300 bytes |
| `static/js/dist/vendor.min.js` | 1,063,275 bytes | 1,115,960 bytes | +52,685 bytes |

Two immutable clean-room installations and builds produce identical files.
The Bootstrap 5 aggregate SHA-256 manifest is:

```text
781757968ebb37421146e4b6a6f21bd121955498326c35eb2314dae9f5085ff2
```

The size increase is explained by replacing the smaller Bootstrap 3 runtime
with Bootstrap 5.3.8's bundle and CSS. No new runtime asset or external
network dependency was introduced.

Retire.js 5.4.3 decreased from 16 source findings (14 medium, 2 low) to
9 findings (8 medium, 1 low). No Bootstrap finding remains; all remaining
findings are limited to the intentionally deferred jQuery and jQuery UI stack.
Yarn and npm audits both report zero known dependency vulnerability, and
Dependabot reports zero open alert.

## Pre-migration stack (historical appendix)

**The following section describes the state audited on 2026-08-19 before the
Bootstrap 5 migration was applied. It is preserved for historical evidence.**

The generated stylesheet was built in this order:

1. Bootstrap CSS 3.3.7;
2. first-party GophishFR styles;
3. the locally modified Flat UI stylesheet;
4. DataTables Bootstrap integration and the remaining component styles.

The browser bundle included Bootstrap JavaScript 3.0.2 and jQuery 1.10.2.
Flat UI JavaScript was not distributed. The resulting UI combined Bootstrap
3.3.7 CSS, older Bootstrap 3.0.2 plugins, and Flat UI overrides based on
Bootstrap 3.1.0 selectors.

## Flat UI provenance

| Evidence | Result |
| --- | --- |
| Authoritative upstream | `designmodo/Flat-UI` |
| Upstream source | `css/flat-ui.css` at commit `097631e59b9950312052123a65cbcbaf97dc740a`, dated 2014-04-04 |
| Upstream metadata | `package.json` and `README.md` both identify Flat UI Free 2.1.3 |
| Upstream CSS identity | Git blob `eef45ea9d9992097faee1451ee5a038536b02f19`; SHA-256 `c4b006ed875ff374794aec7b8e91c4c00550a3387188b66f35014c166c76b5cd`; 4,710 lines and 105,104 bytes |
| Gophish introduction | Commit `9b216c54669598ecfc4bd07ea9439eaabf4fc44e`, dated 2014-05-26 |
| Introduced CSS identity | Git blob `e6cc6ae`; SHA-256 `f38cf6474a7af206e1d97f1474615fc0341f492e7b9d4ffc0381ff4d5598fb4c`; 4,602 lines and 102,710 bytes |
| Import-time differences | 14 insertions and 122 deletions: `fonts/` paths changed to the local `font/` layout, the Flat UI dropdown-menu theme was removed, and one caret margin rule was removed |
| Later local changes | Eight commits changed typography, colors, form borders, navbar behavior, and font size; aggregate current difference from the introduced file is 72 insertions and 122 deletions |
| Classification | `LOCALLY_MODIFIED` derivative of an exact upstream snapshot |
| Confidence | `HIGH` |

The upstream commit is later than the 2.1.3 tag but earlier than the Gophish
import, and no other upstream `flat-ui.css` commit exists between it and the
import date. The file is therefore not an exact tagged-release artifact, but
its source snapshot and upstream version metadata are resolved.

## License

At the exact upstream commit:

- `README.md` says Flat UI Free is licensed under Creative Commons Attribution
  3.0 Unported and the MIT License;
- `package.json` declares `CC BY 3.0 and MIT`;
- the upstream distribution does not assign those terms to separate files;
- no standalone upstream `LICENSE` file existed in 2014.

GophishFR therefore retains both demonstrated upstream terms instead of
inventing a CSS-versus-design-assets split. Redistribution must credit Flat UI
Free and its authors, link the source and CC BY 3.0 license, preserve the MIT
notice, and identify the Gophish/GophishFR modifications.

## Component map

| Component | Files/templates | Flat UI dependency | Bootstrap dependency | JavaScript dependency | Used |
| --- | --- | --- | --- | --- | --- |
| Global typography, links, headings, spacing | Every rendered template | Global element rules and Source Sans Pro/Roboto overrides | Bootstrap reset and grid | None | `USED` |
| Navbar and responsive collapse | `base.html`, `login.html`, `reset_password.html`, `nav.html` | Navbar colors, sizing, caret, and responsive overrides | Navbar, grid, collapse markup | Bootstrap collapse and jQuery | `USED` |
| Buttons and button groups | All application templates | `.btn`, contextual colors, sizes, groups, and caret overrides | Bootstrap button structure and states | Bootstrap dropdown/modal where applicable; jQuery | `USED` |
| Forms and input groups | Login, reset-password, settings, users, campaigns, groups, templates, landing pages, sending profiles, webhooks | Input borders, typography, sizing, validation, and group overrides | Bootstrap forms and grid | First-party jQuery; Select2, DateTimePicker, or File Upload on specific forms | `USED` |
| Navigation tabs | `settings.html`, `campaigns.html`, `templates.html`, `landing_pages.html` | Tab presentation | Bootstrap nav/tab markup | Bootstrap tab and jQuery | `USED` |
| Modals | CRUD templates for campaigns, groups, templates, landing pages, sending profiles, users, and webhooks | Buttons, forms, and typography inside modals | Bootstrap modal layout | Bootstrap modal and jQuery | `USED` |
| Dropdown menus | `campaign_results.html`; navbar/button groups | Remaining caret, inverse-dropdown, and navbar rules | Bootstrap dropdown markup | Bootstrap dropdown and jQuery | `USED` |
| Tooltips | Campaigns, results, groups, settings, templates, landing pages, sending profiles, webhooks | Tooltip colors and arrows | Bootstrap tooltip markup | Bootstrap tooltip and jQuery | `USED` |
| Tables and pagination | Dashboard and every list view | Table typography and pagination theme | Bootstrap tables/pagination | DataTables, its Bootstrap integration, and jQuery | `USED` |
| Labels, badges, alerts, progress bars | Navigation, flash messages, campaign statuses, password forms | Context colors and typography | Bootstrap component structure | Password-strength code for progress bars | `USED` |
| Checkboxes and radios | Settings and CRUD forms | Flat UI icon-based checkbox/radio rules do not match current markup | Bootstrap form structure | Presentation comes from `checkbox.css`; first-party jQuery toggles values | `PARTIALLY_USED` |
| Flat UI custom select | No template or script | `.select` component | None at runtime | Required Flat UI/select plugin is absent; application uses managed Select2 instead | `UNUSED` |
| Tags input and typeahead | No current template or script | `.tagsinput` and `.twitter-typeahead` components | None at runtime | Required plugins are absent | `UNUSED` |
| Pager | No current template or generated DataTables markup | `.pager` component | Bootstrap pager | None | `UNUSED` |
| Flat UI icons | No template or script | `Flat-UI-Icons`, `[data-icon]`, and `fui-*` | None | Font files were never imported | `DEFINITIONS_REMOVED` |
| Bootstrap Switch 1.3 | No template or script | Embedded `.has-switch` and `.switch-square` section | Historical Bootstrap Switch markup | Plugin and mask image were never imported | `REMOVED` |
| Share, palette, tile, todo, video, login screen | No template or script | Flat UI demo components | None in the application | Required images/plugins were never imported | `UNUSED` |
| Flat UI spacing utilities | No current template or script | Final margin/padding utility classes | None | None | `UNUSED` |

The last group of unused components remains globally bundled for now because it
is interleaved with the historical stylesheet. Remove it only in narrowly
reviewable chunks or replace the stylesheet with explicit GophishFR tokens
during the Bootstrap migration.

## Asset and icon map

The introduction commit added Lato font files and rewrote their CSS paths from
`fonts/` to `font/`. Commit `6acbac2` replaced Lato with Source Sans Pro and
Roboto and deleted all Lato files.

No Flat UI icon font, switch mask, tile, todo, video, or login-screen image was
present in the introduction commit or is present now. Font Awesome is the
application icon system. Glyphicons remain indirectly required by Bootstrap
DateTimePicker defaults.

The unused icon `@font-face`, `data-icon`, and concrete `fui-*` glyph definitions
were removed. Dormant generic icon hooks remain inside other Flat UI component
sections; removing those sections belongs to their own component cleanup.

## Safe cleanup result

| Artifact | Before | After |
| --- | --- | --- |
| `static/css/flat-ui.css` | 4,552 lines, 100,225 bytes, SHA-256 `b8748164d7317be382ac11c4797ad5b97d4f3b2510fb884242159200f48b8d4b` | 4,267 lines, 94,080 bytes, SHA-256 `5ede6bc904e9721ad28d893220f82a0008fc3bbb4f5b2efc7f8bd4f00d6f5654` |
| `static/css/dist/gophish.css` | 322,647 bytes, SHA-256 `f43716c7c627eacae3fa1eacc2b1ca12f158274ca9d0ae543d1ab57799e50e38` | 318,028 bytes, SHA-256 `6d20491b35c6d8d29c99b2dfadb0b8a075916d3e9493db45b1b28e0df847e329` |

The 285 deleted source lines are limited to the unused Flat UI icon definition
block and Bootstrap Switch 1.3 block. No dependency or application behavior
changed.

## jQuery map

| Category | Current role |
| --- | --- |
| A. Bootstrap 3 | Required by modal, tab, collapse, dropdown, and tooltip plugins |
| B. Third-party plugins | Required by DataTables, Select2, Bootstrap DateTimePicker, blueimp File Upload, and SweetAlert integration |
| C. GophishFR code | Used throughout all application scripts for DOM queries, events, Ajax, and state updates |
| D. jQuery UI | Only the Widget Factory is shipped, as a dependency of blueimp File Upload |
| E. Unused jQuery | None: the global jQuery runtime is still required |

Bootstrap 5 removes category A, but categories B through D must be migrated or
replaced before jQuery itself can be removed.

## Protected migration invariants

The browser smoke test protects functional rather than pixel-perfect behavior:

- navigation and responsive collapse remain usable;
- forms and primary buttons remain readable and distinguishable;
- modal, tab, dropdown, tooltip, DataTables, Select2, and DateTimePicker
  interactions initialize and operate;
- main layouts, tables, charts, and editor surfaces remain visible;
- no required local asset fails to load.

## Bootstrap 5 blockers and recommended order

Completed in `refactor/migrate-bootstrap5` (see "Migration status" above):

1. Replace the Flat UI monolith with explicit GophishFR design tokens and
   component overrides; do not attempt to load it over Bootstrap 5.
2. Migrate Bootstrap modal, tab, collapse, dropdown, tooltip, navbar, grid, and
   form markup, including `data-*` attributes and removed helper classes.
3. Replace Glyphicons in DateTimePicker and decide whether to retain Font
   Awesome or move to one maintained icon set.
4. Upgrade or replace DataTables Bootstrap integration, Select2 Bootstrap
   theme, DateTimePicker, File Upload, and checkbox presentation.

Deferred (jQuery itself, and the plugins that still require it, are unchanged
by this migration):

5. Refactor first-party jQuery usage after plugin dependencies have been
   removed.
6. Remove jQuery and the remaining jQuery UI Widget Factory last.

The Bootstrap 5 migration was implemented in `refactor/migrate-bootstrap5`;
this audit originally only mapped the pre-migration surface.
