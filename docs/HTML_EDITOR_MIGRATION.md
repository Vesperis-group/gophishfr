# HTML editor migration

## Decision

GophishFR uses CodeMirror 6 as the canonical editor for email-template and
landing-page HTML. A derived, read-only preview replaces CKEditor 4's editable
WYSIWYG mode.

This is an intentional capability change. The old editor could visually modify
rendered content, but switching through that mode could normalize arbitrary
HTML. The new editor prioritizes exact source preservation. Visual inspection
remains available, but all edits are made in the HTML source view.

The persisted string, API JSON fields, database columns, and server-side
template execution are unchanged.

## Contract audited before migration

The CKEditor 4 integration used:

- `fullPage = true`;
- `allowedContent = true`;
- `startupMode = "source"`;
- a custom autocomplete for GophishFR placeholders;
- `getData()` as the value sent to the existing template and page APIs.

The effective content contract therefore includes complete HTML documents,
doctype, `html`, `head`, and `body`, inline and embedded CSS, classes, IDs,
`data-*` and `aria-*` attributes, tables, images, links, forms, inputs, and
GophishFR placeholders in both text and attributes.

Email-template HTML is persisted as the submitted string. Landing-page HTML is
separately parsed by the existing Go `goquery` model code to enforce form
capture policy. That backend behavior is independent of the editor and remains
unchanged.

The pre-migration browser baseline also exposed an existing load-order defect:
CKEditor was created before an existing item's HTML was assigned, so the source
area could open empty. The shared replacement explicitly loads the selected
item after editor creation.

## Candidate comparison

Versions and licensing were checked against official package, repository, and
vendor documentation on 2026-08-19.

| Candidate | License | Result |
| --- | --- | --- |
| CodeMirror 6 | MIT | Selected. It edits a text buffer and therefore does not parse, sanitize, or serialize the stored HTML. |
| Quill 2.0.3 | BSD-3-Clause | Rejected. Its Delta model cannot preserve arbitrary full-document HTML. |
| Lexical 0.49.0 | MIT | Rejected. HTML is converted through a node model and unsupported markup is not guaranteed to round-trip. |
| Tiptap / ProseMirror | MIT core | Rejected. Its schema filters unknown elements and attributes without extensive custom modeling. |
| CKEditor 5 48.4.0 | GPL-2.0-or-later or commercial | Rejected. Licensing needs separate legal review, activation uses a license-key setting, and General HTML Support remains allow-list based. |
| TinyMCE 8.8.2 | GPL-2.0-or-later or commercial | Rejected. Current releases removed the full-page plugin and use a GPL/commercial license-key model. |
| Summernote 0.9.1 | MIT | Rejected. It has no documented full-document fidelity guarantee and has low maintenance velocity. |
| Jodit 4.13.23 | MIT | Rejected. It has no full-document mode; its continuously applied HTML cleaning either removes required content or must be disabled in a same-origin editing surface. |
| GrapesJS | MIT | Rejected. Its component model generates HTML instead of preserving an arbitrary source document. |

No maintained WYSIWYG candidate reviewed can guarantee the source contract
without silent normalization. Building a new WYSIWYG editor is outside this
migration.

## CodeMirror packages

The integration uses granular, exact-pinned packages rather than the deprecated
`@codemirror/basic-setup` package:

- `@codemirror/autocomplete` 6.20.3;
- `@codemirror/commands` 6.11.0;
- `@codemirror/lang-html` 6.4.12;
- `@codemirror/language` 6.12.4;
- `@codemirror/search` 6.7.1;
- `@codemirror/state` 6.7.1;
- `@codemirror/view` 6.43.9.

This explicit set supplies HTML/CSS/JavaScript highlighting, line numbers,
history, search, folding, bracket matching, keyboard commands, selection
feedback, and placeholder completion. It has no CDN, SaaS, telemetry, runtime
service, or commercial key. CodeMirror, Lezer, and the bundled support packages
are MIT-licensed; complete notices are generated at
`static/js/dist/app/html_editor.min.js.LICENSE.txt`.

## Source and placeholder behavior

CodeMirror's document text is the only source of truth. Opening Preview never
writes back to the source buffer. Saving reads `EditorState.doc.toString()`
directly.

Typing `{{.` offers the same placeholder set as the previous integration:

- `RId`
- `FirstName`
- `LastName`
- `Position`
- `From`
- `TrackingURL`
- `Tracker`
- `URL`
- `BaseURL`

## Preview isolation

Preview is generated from a parsed copy of the current source. The canonical
source is never sanitized or rewritten.

The derived copy:

- removes scripts, embedded browsing contexts, objects, refresh directives, and
  external stylesheets;
- removes event-handler and URL-bearing attributes;
- removes CSS imports and `url(...)` resources;
- disables form controls and makes links non-navigable;
- injects a restrictive CSP that denies scripts, connections, frames, forms,
  objects, workers, media, and non-data resources.

The preview iframe has an empty `sandbox` attribute and
`referrerpolicy="no-referrer"`. In particular, it has neither
`allow-scripts` nor `allow-same-origin`. Forms, popups, and top navigation are
not enabled.

These controls apply only to the preview copy. They are not presented as
sanitization of stored templates or landing pages, and they do not replace
backend validation or authorization.

## Regression coverage

The browser suite covers:

- byte-for-byte loading, saving, and reloading of email-template source;
- full documents, placeholders, inline CSS, classes, `data-*`, tables, images,
  links, and scripts in the canonical source;
- placeholder autocomplete;
- landing-page form-policy normalization by the existing backend;
- sandbox and referrer-policy attributes;
- removal of active scripts, handlers, links, and external resources from the
  preview copy;
- absence of external preview requests and unexpected browser errors.
