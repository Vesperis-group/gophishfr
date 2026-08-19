"use strict";

const fs = require("node:fs/promises");
const fsSync = require("node:fs");
const path = require("node:path");

const CleanCSS = require("clean-css");
const { minify } = require("terser");
const webpack = require("webpack");

const webpackConfig = require("../webpack.config");

const projectRoot = path.resolve(__dirname, "..");
const chartPackageDirectory = path.resolve(
  path.dirname(require.resolve("chart.js")),
  "..",
);
const colorPackageDirectory = path.resolve(
  path.dirname(require.resolve("@kurkle/color")),
  "..",
);
const zoomPackageDirectory = path.resolve(
  path.dirname(require.resolve("chartjs-plugin-zoom")),
  "..",
);
const hammerPackageDirectory = path.dirname(require.resolve("hammerjs"));
const zxcvbnDirectory = path.dirname(require.resolve("zxcvbn/package.json"));
const javascriptSourceDirectory = path.join(projectRoot, "static", "js", "src");
const javascriptOutputDirectory = path.join(projectRoot, "static", "js", "dist");
const vendorSourceDirectory = path.join(javascriptSourceDirectory, "vendor");
const stylesheetSourceDirectory = path.join(projectRoot, "static", "css");
const stylesheetOutputDirectory = path.join(stylesheetSourceDirectory, "dist");

function vendoredScript(name) {
  return path.join(vendorSourceDirectory, name);
}

function managedFile(packageName, ...segments) {
  const packageDirectory = path.dirname(
    require.resolve(`${packageName}/package.json`),
  );
  return path.join(packageDirectory, ...segments);
}

function packageDirectory(packageName) {
  let current = path.dirname(require.resolve(packageName));
  while (current !== path.dirname(current)) {
    const manifestPath = path.join(current, "package.json");
    if (fsSync.existsSync(manifestPath)) {
      const manifest = JSON.parse(fsSync.readFileSync(manifestPath, "utf8"));
      if (manifest.name === packageName) {
        return { directory: current, manifest };
      }
    }
    current = path.dirname(current);
  }
  throw new Error(`Unable to locate package directory for ${packageName}`);
}

const vendorScriptPaths = [
  vendoredScript("jquery.js"),
  managedFile("bootstrap", "dist", "js", "bootstrap.bundle.min.js"),
  managedFile("moment", "min", "moment.min.js"),
  managedFile("papaparse", "papaparse.min.js"),
  vendoredScript("d3.min.js"),
  vendoredScript("topojson.min.js"),
  vendoredScript("datamaps.min.js"),
  managedFile("datatables.net", "js", "jquery.dataTables.min.js"),
  managedFile("datatables.net-bs5", "js", "dataTables.bootstrap5.min.js"),
  vendoredScript("datetime-moment.js"),
  vendoredScript("jquery.ui.widget.js"),
  vendoredScript("jquery.fileupload.js"),
  vendoredScript("jquery.iframe-transport.js"),
  vendoredScript("sweetalert2.min.js"),
  vendoredScript("bootstrap-datetime.js"),
  managedFile("select2", "dist", "js", "select2.min.js"),
  vendoredScript("core.min.js"),
  path.join(chartPackageDirectory, "dist", "chart.umd.js"),
  path.join(hammerPackageDirectory, "hammer.min.js"),
  path.join(zoomPackageDirectory, "dist", "chartjs-plugin-zoom.min.js"),
  managedFile("ua-parser-js", "src", "ua-parser.js"),
];

const applicationScripts = [
  "charts.js",
  "campaign_results.js",
  "campaigns.js",
  "dashboard.js",
  "groups.js",
  "landing_pages.js",
  "sending_profiles.js",
  "settings.js",
  "templates.js",
  "gophish.js",
  "users.js",
  "webhooks.js",
];

const stylesheetSources = [
  managedFile("bootstrap", "dist", "css", "bootstrap.min.css"),
  path.join(stylesheetSourceDirectory, "main.css"),
  path.join(stylesheetSourceDirectory, "dashboard.css"),
  path.join(stylesheetSourceDirectory, "gophishfr-theme.css"),
  managedFile("datatables.net-bs5", "css", "dataTables.bootstrap5.min.css"),
  path.join(stylesheetSourceDirectory, "font-awesome.min.css"),
  path.join(stylesheetSourceDirectory, "bootstrap-datetime.css"),
  path.join(stylesheetSourceDirectory, "checkbox.css"),
  path.join(stylesheetSourceDirectory, "sweetalert2.min.css"),
  managedFile("select2", "dist", "css", "select2.min.css"),
];

const managedVendorLicenses = [
  {
    component: "Chart.js 4.5.1",
    sourcePath: path.join(chartPackageDirectory, "LICENSE.md"),
  },
  {
    component: "@kurkle/color 0.3.4",
    sourcePath: path.join(colorPackageDirectory, "LICENSE.md"),
  },
  {
    component: "chartjs-plugin-zoom 2.2.0",
    sourcePath: path.join(zoomPackageDirectory, "LICENSE.md"),
  },
  {
    component: "Hammer.JS 2.0.8",
    sourcePath: path.join(hammerPackageDirectory, "LICENSE.md"),
  },
  {
    component: "Bootstrap 5.3.8",
    sourcePath: managedFile("bootstrap", "LICENSE"),
  },
  {
    component: "@popperjs/core 2.11.8",
    sourcePath: managedFile("@popperjs/core", "LICENSE.md"),
  },
  {
    component: "DataTables and DataTables Bootstrap 5 integration 1.13.11",
    sourcePath: managedFile("datatables.net", "License.txt"),
  },
  {
    component: "Moment.js 2.30.1",
    sourcePath: managedFile("moment", "LICENSE"),
  },
  {
    component: "Papa Parse 5.6.0",
    sourcePath: managedFile("papaparse", "LICENSE"),
  },
  {
    component: "Select2 4.0.13",
    sourcePath: managedFile("select2", "LICENSE.md"),
  },
  {
    component: "UAParser.js 0.7.41",
    sourcePath: managedFile("ua-parser-js", "license.md"),
  },
];

const editorPackages = [
  "@codemirror/autocomplete",
  "@codemirror/commands",
  "@codemirror/lang-css",
  "@codemirror/lang-html",
  "@codemirror/lang-javascript",
  "@codemirror/language",
  "@codemirror/lint",
  "@codemirror/search",
  "@codemirror/state",
  "@codemirror/view",
  "@lezer/common",
  "@lezer/css",
  "@lezer/highlight",
  "@lezer/html",
  "@lezer/javascript",
  "@lezer/lr",
  "@marijn/find-cluster-break",
  "crelt",
  "style-mod",
  "w3c-keyname",
];

async function minifyJavaScript(source, sourceName) {
  const result = await minify(source);
  if (typeof result.code !== "string") {
    throw new Error(`Terser produced no output for ${sourceName}`);
  }
  return result.code;
}

async function buildVendorScripts() {
  const sources = await Promise.all(
    vendorScriptPaths.map((sourcePath) => fs.readFile(sourcePath, "utf8")),
  );
  const output = await minifyJavaScript(sources.join("\n"), "vendor scripts");
  await fs.writeFile(
    path.join(javascriptOutputDirectory, "vendor.min.js"),
    output,
  );
}

async function buildManagedVendorLicenses() {
  const notices = await Promise.all(
    managedVendorLicenses.map(async ({ component, sourcePath }) => {
      const license = await fs.readFile(sourcePath, "utf8");
      return `${component}\n${"=".repeat(component.length)}\n\n${license.trim()}`;
    }),
  );
  await fs.writeFile(
    path.join(javascriptOutputDirectory, "vendor.min.js.LICENSE.txt"),
    `${notices.join("\n\n")}\n`,
  );
}

async function buildEditorLicenses() {
  const notices = await Promise.all(
    editorPackages.map(async (packageName) => {
      const { directory, manifest } = packageDirectory(packageName);
      const license = await fs.readFile(
        path.join(directory, "LICENSE"),
        "utf8",
      );
      const heading = `${packageName} ${manifest.version}`;
      return `${heading}\n${"=".repeat(heading.length)}\n\n${license.trim()}`;
    }),
  );
  await fs.writeFile(
    path.join(
      javascriptOutputDirectory,
      "app",
      "html_editor.min.js.LICENSE.txt",
    ),
    `${notices.join("\n\n")}\n`,
  );
}

async function buildApplicationScripts() {
  await Promise.all(
    applicationScripts.map(async (name) => {
      const sourcePath = path.join(javascriptSourceDirectory, "app", name);
      const source = await fs.readFile(sourcePath, "utf8");
      const output = await minifyJavaScript(source, name);
      const outputName = name.replace(/\.js$/, ".min.js");
      await fs.writeFile(
        path.join(javascriptOutputDirectory, "app", outputName),
        output,
      );
    }),
  );
}

async function buildStylesheets() {
  const outputs = await Promise.all(
    stylesheetSources.map(async (sourcePath) => {
      const source = await fs.readFile(sourcePath, "utf8");
      const result = new CleanCSS({ level: 1 }).minify(source);
      if (result.errors.length > 0) {
        throw new Error(
          `CleanCSS failed for ${sourcePath}: ${result.errors.join("; ")}`,
        );
      }
      for (const warning of result.warnings) {
        console.warn(`CleanCSS warning for ${sourcePath}: ${warning}`);
      }
      return result.styles;
    }),
  );
  await fs.writeFile(
    path.join(stylesheetOutputDirectory, "gophish.css"),
    outputs.join("\n"),
  );
}

async function buildWebpackScripts() {
  const compiler = webpack(webpackConfig);
  await new Promise((resolve, reject) => {
    compiler.run((runError, stats) => {
      compiler.close((closeError) => {
        if (runError) {
          reject(runError);
          return;
        }
        if (closeError) {
          reject(closeError);
          return;
        }
        if (!stats) {
          reject(new Error("Webpack produced no compilation statistics"));
          return;
        }
        if (stats.hasErrors()) {
          reject(
            new Error(
              stats.toString({
                all: false,
                errors: true,
                errorDetails: true,
              }),
            ),
          );
          return;
        }
        if (stats.hasWarnings()) {
          console.warn(
            stats.toString({
              all: false,
              warnings: true,
            }),
          );
        }
        resolve();
      });
    });
  });
  await fs.copyFile(
    path.join(zxcvbnDirectory, "LICENSE.txt"),
    path.join(
      javascriptOutputDirectory,
      "app",
      "passwords.min.js.LICENSE.txt",
    ),
  );
  await buildEditorLicenses();
}

async function build() {
  await Promise.all([
    fs.rm(javascriptOutputDirectory, { recursive: true, force: true }),
    fs.rm(stylesheetOutputDirectory, { recursive: true, force: true }),
  ]);
  await Promise.all([
    fs.mkdir(path.join(javascriptOutputDirectory, "app"), { recursive: true }),
    fs.mkdir(stylesheetOutputDirectory, { recursive: true }),
  ]);
  await Promise.all([
    buildVendorScripts(),
    buildManagedVendorLicenses(),
    buildApplicationScripts(),
    buildStylesheets(),
    buildWebpackScripts(),
  ]);
}

build().catch((error) => {
  console.error(error instanceof Error ? error.stack : error);
  process.exitCode = 1;
});
