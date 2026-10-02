import { defineConfig } from "@playwright/test";

const baseURL = process.env.GOPHISHFR_BROWSER_BASE_URL;
const outputDir = process.env.GOPHISHFR_BROWSER_OUTPUT_DIR;

if (!baseURL || !outputDir) {
  throw new Error(
    "Browser tests must run through the Go test harness so the isolated server and output directory are configured.",
  );
}

const serverURL = new URL(baseURL);
if (
  serverURL.protocol !== "http:" ||
  !["127.0.0.1", "localhost"].includes(serverURL.hostname)
) {
  throw new Error("Browser tests may only target a local HTTP server.");
}

// Dedicated config for event-details-purge.spec.ts only (see that file's
// header comment and controllers/browser_event_details_purge_test.go).
// This file is deliberately separate from playwright.config.ts (the
// default config, which explicitly ignores event-details-purge.spec.ts):
// that spec must run exactly twice, in two separate `yarn playwright test`
// invocations against the SAME server, with a real purge executed between
// them -- it must never be swept up by TestBrowserSmoke's single,
// unrestricted, all-spec-files invocation of the default config.
export default defineConfig({
  testDir: ".",
  testMatch: /event-details-purge\.spec\.ts$/,
  outputDir,
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: [["line"]],
  use: {
    baseURL,
    browserName: "chromium",
    headless: true,
    serviceWorkers: "block",
    trace: "off",
    screenshot: "off",
    video: "off",
  },
});
