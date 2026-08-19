import {
  autocompletion,
  closeBrackets,
  closeBracketsKeymap,
  completionKeymap,
} from "@codemirror/autocomplete";
import {
  defaultKeymap,
  history,
  historyKeymap,
  indentWithTab,
} from "@codemirror/commands";
import { html } from "@codemirror/lang-html";
import {
  bracketMatching,
  defaultHighlightStyle,
  foldGutter,
  foldKeymap,
  indentOnInput,
  syntaxHighlighting,
} from "@codemirror/language";
import { highlightSelectionMatches, searchKeymap } from "@codemirror/search";
import { EditorState } from "@codemirror/state";
import {
  crosshairCursor,
  drawSelection,
  dropCursor,
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
  rectangularSelection,
} from "@codemirror/view";

const TEMPLATE_TAGS = Object.freeze([
  {
    name: "RId",
    description: "The unique ID for the recipient.",
  },
  {
    name: "FirstName",
    description: "The recipient's first name.",
  },
  {
    name: "LastName",
    description: "The recipient's last name.",
  },
  {
    name: "Position",
    description: "The recipient's position.",
  },
  {
    name: "From",
    description: "The address emails are sent from.",
  },
  {
    name: "TrackingURL",
    description: "The URL to track emails being opened.",
  },
  {
    name: "Tracker",
    description: "An HTML tag that adds a hidden tracking image.",
  },
  {
    name: "URL",
    description: "The URL to the GophishFR listener.",
  },
  {
    name: "BaseURL",
    description: "The listener URL without the path and recipient parameter.",
  },
]);

const PREVIEW_CSP = [
  "default-src 'none'",
  "base-uri 'none'",
  "child-src 'none'",
  "connect-src 'none'",
  "font-src data:",
  "form-action 'none'",
  "frame-src 'none'",
  "img-src data:",
  "media-src 'none'",
  "object-src 'none'",
  "script-src 'none'",
  "style-src 'unsafe-inline'",
  "worker-src 'none'",
].join("; ");

const URL_ATTRIBUTES = new Set([
  "action",
  "background",
  "cite",
  "data",
  "formaction",
  "href",
  "manifest",
  "ping",
  "poster",
  "src",
  "srcdoc",
  "srcset",
  "xlink:href",
]);

function placeholderCompletionSource(context) {
  const token = context.matchBefore(/\{\{\.[A-Za-z]*$/);
  if (!token) {
    return null;
  }

  return {
    from: token.from,
    options: TEMPLATE_TAGS.map(({ name, description }) => ({
      label: `{{.${name}}}`,
      apply(view, completion, from, to) {
        const trailingBraces = view.state.sliceDoc(to, to + 2) === "}}";
        const end = trailingBraces ? to + 2 : to;
        view.dispatch({
          changes: {
            from,
            to: end,
            insert: completion.label,
          },
          selection: {
            anchor: from + completion.label.length,
          },
        });
      },
      detail: description,
      type: "variable",
    })),
  };
}

function removeCSSResources(css) {
  return css
    .replace(/@import\b[^;]*(?:;|$)/gi, "")
    .replace(/url\s*\([^)]*\)/gi, "none");
}

function buildPreviewDocument(source) {
  const preview = new DOMParser().parseFromString(source, "text/html");

  preview
    .querySelectorAll(
      "base, embed, frame, frameset, iframe, link, meta[http-equiv='refresh' i], object, portal, script",
    )
    .forEach((element) => element.remove());

  preview.querySelectorAll("*").forEach((element) => {
    for (const attribute of Array.from(element.attributes)) {
      const name = attribute.name.toLowerCase();
      if (name.startsWith("on")) {
        element.removeAttribute(attribute.name);
        continue;
      }
      if (URL_ATTRIBUTES.has(name)) {
        element.setAttribute(`data-preview-${name.replace(":", "-")}`, attribute.value);
        element.removeAttribute(attribute.name);
      }
    }

    if (element.hasAttribute("style")) {
      element.setAttribute(
        "style",
        removeCSSResources(element.getAttribute("style") || ""),
      );
    }
  });

  preview.querySelectorAll("style").forEach((style) => {
    style.textContent = removeCSSResources(style.textContent || "");
  });

  preview.querySelectorAll("form").forEach((form) => {
    form.setAttribute("inert", "");
    form.setAttribute("aria-disabled", "true");
  });
  preview
    .querySelectorAll("button, input, select, textarea")
    .forEach((control) => control.setAttribute("disabled", ""));
  preview.querySelectorAll("a").forEach((link) => {
    link.setAttribute("aria-disabled", "true");
    link.setAttribute("tabindex", "-1");
  });

  const csp = preview.createElement("meta");
  csp.setAttribute("http-equiv", "Content-Security-Policy");
  csp.setAttribute("content", PREVIEW_CSP);
  preview.head.prepend(csp);

  return `<!doctype html>\n${preview.documentElement.outerHTML}`;
}

class HTMLEditor {
  constructor(textarea) {
    this.textarea = textarea;
    this.root = textarea.closest("[data-html-editor]");
    if (!this.root) {
      throw new Error(`Missing editor container for #${textarea.id}`);
    }

    this.sourcePanel = this.root.querySelector('[data-editor-panel="source"]');
    this.previewPanel = this.root.querySelector('[data-editor-panel="preview"]');
    this.previewFrame = this.root.querySelector("iframe[data-editor-preview]");
    this.previewTimer = null;
    this.activeView = "source";

    if (!this.sourcePanel || !this.previewPanel || !this.previewFrame) {
      throw new Error(`Incomplete editor markup for #${textarea.id}`);
    }

    this.mount = document.createElement("div");
    this.mount.className = "gophish-code-editor";
    textarea.hidden = true;
    textarea.setAttribute("aria-hidden", "true");
    textarea.insertAdjacentElement("afterend", this.mount);

    this.extensions = [
      lineNumbers(),
      highlightActiveLineGutter(),
      history(),
      foldGutter(),
      drawSelection(),
      dropCursor(),
      EditorState.allowMultipleSelections.of(true),
      indentOnInput(),
      syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
      bracketMatching(),
      closeBrackets(),
      autocompletion({
        activateOnTyping: true,
        override: [placeholderCompletionSource],
      }),
      rectangularSelection(),
      crosshairCursor(),
      highlightActiveLine(),
      highlightSelectionMatches(),
      html(),
      EditorView.lineWrapping,
      EditorView.contentAttributes.of({
        "aria-label": textarea.dataset.editorLabel || "HTML source",
      }),
      EditorView.updateListener.of((update) => {
        if (!update.docChanged) {
          return;
        }
        this.textarea.value = update.state.doc.toString();
        if (this.activeView === "preview") {
          this.schedulePreview();
        }
      }),
      keymap.of([
        ...closeBracketsKeymap,
        ...defaultKeymap,
        ...searchKeymap,
        ...historyKeymap,
        ...foldKeymap,
        ...completionKeymap,
        indentWithTab,
      ]),
    ];

    this.view = new EditorView({
      parent: this.mount,
      state: this.createState(textarea.value),
    });

    this.root.querySelectorAll("[data-editor-view]").forEach((button) => {
      button.addEventListener("click", () => {
        this.show(button.dataset.editorView);
      });
    });
  }

  createState(value) {
    return EditorState.create({
      doc: value,
      extensions: this.extensions,
    });
  }

  getData() {
    return this.view.state.doc.toString();
  }

  setData(value) {
    const source = typeof value === "string" ? value : "";
    this.textarea.value = source;
    this.view.setState(this.createState(source));
    if (this.activeView === "preview") {
      this.renderPreview();
    }
  }

  show(viewName) {
    const nextView = viewName === "preview" ? "preview" : "source";
    this.activeView = nextView;

    this.root.querySelectorAll("[data-editor-view]").forEach((button) => {
      const active = button.dataset.editorView === nextView;
      button.classList.toggle("active", active);
      button.setAttribute("aria-selected", String(active));
    });

    this.sourcePanel.hidden = nextView !== "source";
    this.previewPanel.hidden = nextView !== "preview";
    if (nextView === "preview") {
      this.renderPreview();
      return;
    }

    this.view.requestMeasure();
    this.view.focus();
  }

  showPreview() {
    this.show("preview");
  }

  showSource() {
    this.show("source");
  }

  schedulePreview() {
    window.clearTimeout(this.previewTimer);
    this.previewTimer = window.setTimeout(() => this.renderPreview(), 250);
  }

  renderPreview() {
    window.clearTimeout(this.previewTimer);
    this.previewFrame.srcdoc = buildPreviewDocument(this.getData());
  }

  focus() {
    this.showSource();
  }
}

const instances = new Map();

window.GophishHTMLEditor = Object.freeze({
  create(id) {
    if (instances.has(id)) {
      return instances.get(id);
    }

    const textarea = document.getElementById(id);
    if (!(textarea instanceof HTMLTextAreaElement)) {
      throw new Error(`HTML editor textarea #${id} was not found`);
    }

    const editor = new HTMLEditor(textarea);
    instances.set(id, editor);
    return editor;
  },

  get(id) {
    return instances.get(id);
  },
});
