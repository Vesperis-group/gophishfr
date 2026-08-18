"use strict";

const fs = require("node:fs/promises");
const path = require("node:path");

const CleanCSS = require("clean-css");
const { minify } = require("terser");
const webpack = require("webpack");

const webpackConfig = require("../webpack.config");

const projectRoot = path.resolve(__dirname, "..");
const zxcvbnDirectory = path.dirname(require.resolve("zxcvbn/package.json"));
const javascriptSourceDirectory = path.join(projectRoot, "static", "js", "src");
const javascriptOutputDirectory = path.join(projectRoot, "static", "js", "dist");
const stylesheetSourceDirectory = path.join(projectRoot, "static", "css");
const stylesheetOutputDirectory = path.join(stylesheetSourceDirectory, "dist");

const vendorScripts = [
  "jquery.js",
  "bootstrap.min.js",
  "moment.min.js",
  "papaparse.min.js",
  "d3.min.js",
  "topojson.min.js",
  "datamaps.min.js",
  "jquery.dataTables.min.js",
  "dataTables.bootstrap.js",
  "datetime-moment.js",
  "jquery.ui.widget.js",
  "jquery.fileupload.js",
  "jquery.iframe-transport.js",
  "sweetalert2.min.js",
  "bootstrap-datetime.js",
  "select2.min.js",
  "core.min.js",
  "highcharts.js",
  "ua-parser.min.js",
];

const applicationScripts = [
  "autocomplete.js",
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

const stylesheets = [
  "bootstrap.min.css",
  "main.css",
  "dashboard.css",
  "flat-ui.css",
  "dataTables.bootstrap.css",
  "font-awesome.min.css",
  "chartist.min.css",
  "bootstrap-datetime.css",
  "checkbox.css",
  "sweetalert2.min.css",
  "select2.min.css",
  "select2-bootstrap.min.css",
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
    vendorScripts.map((name) =>
      fs.readFile(path.join(javascriptSourceDirectory, "vendor", name), "utf8"),
    ),
  );
  const output = await minifyJavaScript(sources.join("\n"), "vendor scripts");
  await fs.writeFile(path.join(javascriptOutputDirectory, "vendor.min.js"), output);
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
    stylesheets.map(async (name) => {
      const source = await fs.readFile(
        path.join(stylesheetSourceDirectory, name),
        "utf8",
      );
      const result = new CleanCSS({ level: 1 }).minify(source);
      if (result.errors.length > 0) {
        throw new Error(`CleanCSS failed for ${name}: ${result.errors.join("; ")}`);
      }
      for (const warning of result.warnings) {
        console.warn(`CleanCSS warning for ${name}: ${warning}`);
      }
      return result.styles;
    }),
  );
  await fs.writeFile(
    path.join(stylesheetOutputDirectory, "gophish.css"),
    outputs.join("\n"),
  );
}

async function buildPasswordScript() {
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
    buildApplicationScripts(),
    buildStylesheets(),
    buildPasswordScript(),
  ]);
}

build().catch((error) => {
  console.error(error instanceof Error ? error.stack : error);
  process.exitCode = 1;
});
