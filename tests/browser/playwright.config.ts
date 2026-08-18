import { defineConfig } from "@playwright/test";

const baseURL = process.env.GOPHISHFR_BROWSER_BASE_URL;
const outputDir = process.env.GOPHISHFR_BROWSER_OUTPUT_DIR;

if (!baseURL || !outputDir) {
  throw new Error(
    "Browser tests must run through yarn test:browser so the isolated server and output directory are configured.",
  );
}

const serverURL = new URL(baseURL);
if (
  serverURL.protocol !== "http:" ||
  !["127.0.0.1", "localhost"].includes(serverURL.hostname)
) {
  throw new Error("Browser tests may only target a local HTTP server.");
}

export default defineConfig({
  testDir: ".",
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
