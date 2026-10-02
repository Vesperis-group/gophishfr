import { expect, test, type Page } from "@playwright/test";

// This spec is the real-browser regression test for goal.md's event-details
// purge PR (.goals/event-details-purge/goal.md item 8). It is deliberately
// NOT part of the shared frontend-smoke suite: it is invoked twice, with the
// SAME running server, by controllers' TestBrowserEventDetailsPurge --
// once with GOPHISHFR_BROWSER_PURGE_PHASE=before (prior to any purge) and
// once with =after (following a real, direct
// models.PurgeEventDetailsBefore call made by that Go test in between the
// two Playwright invocations). See
// tests/browser/playwright.purge.config.ts, and
// tests/browser/playwright.config.ts's testIgnore entry for this file (so
// the shared TestBrowserSmoke suite never also tries to run it against its
// own, differently-shaped fixture data).

const username = requiredEnvironmentVariable("GOPHISHFR_BROWSER_USERNAME");
const password = requiredEnvironmentVariable("GOPHISHFR_BROWSER_PASSWORD");
const campaignId = requiredEnvironmentVariable("GOPHISHFR_BROWSER_PURGE_CAMPAIGN_ID");
const phase = requiredEnvironmentVariable("GOPHISHFR_BROWSER_PURGE_PHASE");
if (phase !== "before" && phase !== "after") {
  throw new Error(`GOPHISHFR_BROWSER_PURGE_PHASE must be "before" or "after", got ${phase}`);
}

function requiredEnvironmentVariable(name: string): string {
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

async function login(page: Page): Promise<void> {
  await page.goto("/login");
  await page.locator('input[name="username"]').fill(username);
  await page.locator('input[name="password"]').fill(password);
  await Promise.all([
    page.waitForURL((url) => url.pathname === "/"),
    page.getByRole("button", { name: "Sign in" }).click(),
  ]);
}

test(`campaign results Replay/CSV before vs after an event-details purge (${phase})`, async ({
  page,
}) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  const consoleErrors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") {
      consoleErrors.push(message.text());
    }
  });

  await login(page);
  await page.goto(`/campaigns/${campaignId}`);
  await expect(page.locator("#resultsTable")).toBeVisible();
  await expect(page.locator("#resultsTable tbody tr")).toHaveCount(1);

  // Expand the single result's timeline. Exactly one row exists (one
  // target), so there is no ambiguity about which row this is.
  const detailsCell = page.locator("#resultsTable tbody td.details-control").first();
  await detailsCell.locator("i").click();

  const timeline = page.locator(".timeline");
  await expect(timeline).toBeVisible();

  // The Submitted Data entry's message/time must be present and unchanged
  // regardless of phase -- a purge clears Details, never the event's other
  // fields.
  const submittedEntry = timeline
    .locator(".timeline-entry")
    .filter({ hasText: "Submitted Data" });
  await expect(submittedEntry).toHaveCount(1);

  const replayButton = submittedEntry.getByRole("button", { name: "Replay Credentials" });

  if (phase === "before") {
    // Pre-purge: Replay must be offered and functional, exactly like any
    // other Submitted Data event with captured credentials.
    await expect(replayButton).toBeVisible();

    await page.evaluate(() => {
      const globals = window as unknown as {
        __submittedReplay?: { action: string; fields: Array<[string, string]> };
        __submitBeforeReplay?: typeof HTMLFormElement.prototype.submit;
      };
      globals.__submitBeforeReplay = HTMLFormElement.prototype.submit;
      HTMLFormElement.prototype.submit = function () {
        globals.__submittedReplay = {
          action: this.action,
          fields: Array.from(this.elements)
            .filter((element): element is HTMLInputElement => element instanceof HTMLInputElement)
            .map((input) => [input.name, input.value]),
        };
      };
    });

    await replayButton.click();
    await page.locator(".swal2-input").fill("https://purge-replay-target.invalid/submit");
    await page.locator(".swal2-confirm").click();

    await expect
      .poll(() =>
        page.evaluate(
          () =>
            (window as unknown as { __submittedReplay?: { action: string } })
              .__submittedReplay?.action,
        ),
      )
      .toBe("https://purge-replay-target.invalid/submit");

    const replayState = await page.evaluate(() => {
      const globals = window as unknown as {
        __submittedReplay?: { action: string; fields: Array<[string, string]> };
        __submitBeforeReplay?: typeof HTMLFormElement.prototype.submit;
      };
      const state = globals.__submittedReplay;
      if (globals.__submitBeforeReplay) {
        HTMLFormElement.prototype.submit = globals.__submitBeforeReplay;
      }
      delete globals.__submitBeforeReplay;
      delete globals.__submittedReplay;
      return state;
    });
    expect(replayState?.action).toBe("https://purge-replay-target.invalid/submit");
    expect(replayState?.fields).toContainEqual(["username", "purge-replay-user"]);
  } else {
    // Post-purge: Replay must be ABSENT (the event's Details is now the
    // empty string, indistinguishable by design from an event that never
    // had any -- see goal.md item 0/6), with zero exceptions anywhere.
    await expect(replayButton).toHaveCount(0);
    await expect(submittedEntry.locator(".timeline-device-details")).toHaveCount(0);
    await expect(submittedEntry.locator(".timeline-event-results")).toHaveCount(0);
  }

  // CSV export: a purged event's row must have an empty "details" cell and
  // intact email/message, with no exception either way.
  const exportState = await page.evaluate(() => {
    type ExportGlobals = Window & {
      Papa: {
        unparse: (input: unknown, options: { escapeFormulae: boolean }) => string;
      };
      exportAsCSV: (scope: string) => void;
    };
    const globals = window as unknown as ExportGlobals;
    const originalUnparse = globals.Papa.unparse;
    const originalCreateObjectURL = URL.createObjectURL;
    const originalClick = HTMLAnchorElement.prototype.click;
    let captured: unknown[] = [];
    try {
      globals.Papa.unparse = (input, options) => {
        captured = input as unknown[];
        return originalUnparse(input, options);
      };
      URL.createObjectURL = () => "blob:purge-browser-test";
      HTMLAnchorElement.prototype.click = function () {};
      globals.exportAsCSV("events");
      return captured;
    } finally {
      globals.Papa.unparse = originalUnparse;
      URL.createObjectURL = originalCreateObjectURL;
      HTMLAnchorElement.prototype.click = originalClick;
    }
  });
  const submittedRow = (exportState as Array<Record<string, unknown>>).find(
    (row) => row.message === "Submitted Data",
  );
  expect(submittedRow).toBeTruthy();
  expect(submittedRow?.email).toBe("purge-fixture@localhost.invalid");
  if (phase === "after") {
    expect(submittedRow?.details).toBe("");
  }

  expect(pageErrors, `uncaught page errors: ${pageErrors.join("; ")}`).toEqual([]);
  expect(consoleErrors, `console errors: ${consoleErrors.join("; ")}`).toEqual([]);
});
