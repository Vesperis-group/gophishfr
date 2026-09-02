import path from "node:path";

import { expect, test, type Page } from "@playwright/test";

// This file proves one property in isolation: the table library the application
// ships works with no jQuery on the page at all.
//
// The exact files that go into the shipped bundle are injected into a blank
// page, jQuery is deliberately absent, and the table is driven only through the
// API the application uses.

const moduleRoot = path.join(__dirname, "..", "..", "node_modules");
const vendorScripts = [
  path.join(moduleRoot, "moment", "min", "moment.min.js"),
  path.join(moduleRoot, "datatables.net", "js", "dataTables.min.js"),
  path.join(moduleRoot, "datatables.net-bs5", "js", "dataTables.bootstrap5.min.js"),
];

const rows = [
  ["Delta", "March 4th 2031, 9:00:00 am"],
  ["alpha", "March 12th 2031, 9:00:00 am"],
  ["Charlie", "March 21st 2031, 9:00:00 am"],
  ["Bravo", "March 9th 2031, 9:00:00 am"],
];

async function visibleNames(page: Page): Promise<string[]> {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll("#isolated tbody tr"))
      .filter((row) => !Array.from(row.querySelectorAll("td")).some((c) => c.colSpan > 1))
      .map((row) => (row.querySelector("td")?.textContent ?? "").trim()),
  );
}

test("the table library works with no jQuery on the page", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(String(error)));
  page.on("console", (message) => {
    if (message.type() === "error") {
      pageErrors.push(`console: ${message.text()}`);
    }
  });

  await page.setContent(`<!doctype html><html><head><meta charset="utf-8"></head>
    <body><table id="isolated" class="table"><thead><tr>
      <th>Name</th><th>Modified Date</th>
    </tr></thead><tbody></tbody></table></body></html>`);

  for (const script of vendorScripts) {
    await page.addScriptTag({ path: script });
  }

  // The premise of this test: nothing jQuery-shaped is present.
  expect(
    await page.evaluate(() => ({
      jquery: typeof (window as Window & { jQuery?: unknown }).jQuery,
      dollar: typeof (window as Window & { $?: unknown }).$,
      dataTable: typeof (window as Window & { DataTable?: unknown }).DataTable,
    })),
  ).toEqual({ jquery: "undefined", dollar: "undefined", dataTable: "function" });

  const version = await page.evaluate(
    () => (window as Window & { DataTable: { version: string } }).DataTable.version,
  );
  expect(version).toBe("3.0.3");

  // Initialisation through the API the application uses, with the same date
  // type registration and ordering sequence.
  await page.evaluate((data) => {
    const global = window as Window & {
      DataTable: {
        new (selector: string, options: unknown): unknown;
        datetime: (format: string) => void;
        defaults: { column: { orderSequence: string[] } };
      };
      isolatedTable: unknown;
    };
    global.DataTable.datetime("MMMM Do YYYY, h:mm:ss a");
    global.DataTable.defaults.column.orderSequence = ["asc", "desc"];
    const table = new global.DataTable("#isolated", {
      pageLength: 2,
      order: [[0, "asc"]],
    }) as { clear: () => { rows: { add: (r: string[][]) => { draw: () => void } } } };
    table.clear().rows.add(data).draw();
    global.isolatedTable = table;
  }, rows);

  expect(pageErrors).toEqual([]);

  // Ordering: case-insensitive on the name column.
  expect(await visibleNames(page)).toEqual(["alpha", "Bravo"]);

  // Paging: the second page holds the rest, in the same order. The next and
  // previous controls are used rather than numbered buttons, because how many
  // numbers are rendered depends on the available width.
  await page.getByRole("link", { name: "Next" }).click();
  await expect.poll(() => visibleNames(page)).toEqual(["Charlie", "Delta"]);
  await page.getByRole("link", { name: "Previous" }).click();
  await expect.poll(() => visibleNames(page)).toEqual(["alpha", "Bravo"]);

  // Ordering by the date column must be chronological rather than
  // lexicographic, which is what the registered date type provides.
  await page.locator("#isolated thead th").nth(1).click();
  await expect.poll(() => visibleNames(page)).toEqual(["Delta", "Bravo"]);

  // Searching narrows the rows through the generated control.
  await page.locator("#isolated thead th").first().click();
  await page.locator(".dt-search input").fill("ravo");
  await expect.poll(() => visibleNames(page)).toEqual(["Bravo"]);
  await page.locator(".dt-search input").fill("");
  await expect.poll(() => visibleNames(page)).toEqual(["alpha", "Bravo"]);

  // Still no jQuery, and nothing threw along the way.
  expect(
    await page.evaluate(() => typeof (window as Window & { jQuery?: unknown }).jQuery),
  ).toBe("undefined");
  expect(pageErrors).toEqual([]);
});
