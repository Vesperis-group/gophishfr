import { expect, test } from "@playwright/test";

const baseURL = requiredEnvironmentVariable("GOPHISHFR_BROWSER_BASE_URL");
const username = requiredEnvironmentVariable("GOPHISHFR_BROWSER_USERNAME");
const password = requiredEnvironmentVariable("GOPHISHFR_BROWSER_PASSWORD");
const localOrigin = new URL(baseURL).origin;
const localServer = new URL(baseURL);

function requiredEnvironmentVariable(name: string): string {
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

test("legacy frontend browser smoke baseline", async ({ context, page }) => {
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  const failedLocalRequests: string[] = [];
  const failedLocalResponses: string[] = [];
  const unexpectedExternalRequests: string[] = [];
  const loadedAssets = new Set<string>();

  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error") {
      consoleErrors.push(message.text());
    }
  });
  page.on("requestfailed", (request) => {
    const url = new URL(request.url());
    if (url.origin === localOrigin) {
      failedLocalRequests.push(
        `${request.method()} ${url.pathname}: ${request.failure()?.errorText}`,
      );
    }
  });
  page.on("response", (response) => {
    const url = new URL(response.url());
    if (url.origin !== localOrigin) {
      return;
    }
    if (response.status() >= 400) {
      failedLocalResponses.push(`${response.status()} ${url.pathname}`);
    }
    if (["script", "stylesheet", "image", "font"].includes(response.request().resourceType())) {
      loadedAssets.add(url.pathname);
    }
  });

  await context.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin === localOrigin || ["data:", "blob:"].includes(url.protocol)) {
      await route.continue();
      return;
    }
    if (
      url.protocol === "https:" &&
      url.hostname === "fonts.googleapis.com" &&
      request.resourceType() === "stylesheet"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "text/css",
        body: "",
      });
      return;
    }
    unexpectedExternalRequests.push(request.url());
    await route.abort("blockedbyclient");
  });
  await context.routeWebSocket("**/*", async (webSocket) => {
    const url = new URL(webSocket.url());
    const expectedProtocol = localServer.protocol === "http:" ? "ws:" : "wss:";
    if (
      url.protocol === expectedProtocol &&
      url.hostname === localServer.hostname &&
      url.port === localServer.port
    ) {
      webSocket.connectToServer();
      return;
    }
    unexpectedExternalRequests.push(webSocket.url());
    await webSocket.close({
      code: 1008,
      reason: "Browser smoke tests block non-local WebSockets",
    });
  });

  await test.step("login loads legacy CSS and JavaScript", async () => {
    const response = await page.goto("/login");
    expect(response?.status()).toBe(200);
    await expect(page).toHaveTitle("GophishFR - Login");
    await expect(page.locator("form.form-signin")).toBeVisible();
    await expect(page.locator("form.form-signin")).toHaveCSS("max-width", "400px");
    await expect(page.locator('input[name="username"]')).toBeVisible();
    await expect(page.locator('input[name="password"]')).toBeVisible();
    await expect(page.locator('input[name="csrf_token"]')).toHaveCount(1);
    expect(
      await page.evaluate(
        () =>
          typeof window.jQuery === "function" &&
          typeof window.jQuery.fn.modal === "function" &&
          typeof window.jQuery.fn.DataTable === "function",
      ),
    ).toBe(true);
  });

  await test.step("synthetic login redirects to the dashboard", async () => {
    await page.locator('input[name="username"]').fill(username);
    await page.locator('input[name="password"]').fill(password);
    await Promise.all([
      page.waitForURL((url) => url.pathname === "/"),
      page.getByRole("button", { name: "Sign in" }).click(),
    ]);
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
    await expect(page.locator("#emptyMessage")).toBeVisible();
    await expect(page.locator("#navbar-dropdown")).toContainText(username);
  });

  await test.step("campaign controls initialize Bootstrap and jQuery plugins", async () => {
    await page.getByRole("link", { name: "Campaigns", exact: true }).click();
    await expect(page).toHaveURL(/\/campaigns$/);
    await expect(page.getByRole("heading", { name: "Campaigns" })).toBeVisible();
    await expect(page.locator("#emptyMessage").first()).toBeVisible();

    await page.locator('a[href="#archivedCampaigns"]').click();
    await expect(page.locator("#archivedCampaigns")).toHaveClass(/active/);

    await page.getByRole("button", { name: "New Campaign" }).click();
    await expect(page.locator("#modal")).toHaveClass(/in/);
    await expect(page.locator("#modal")).toBeVisible();
    await expect(page.locator(".select2-container")).toHaveCount(4);
    expect(
      await page.evaluate(
        () =>
          typeof window.jQuery.fn.select2 === "function" &&
          typeof window.jQuery.fn.datetimepicker === "function" &&
          window.jQuery("#launch_date").data("DateTimePicker") !== undefined,
      ),
    ).toBe(true);
    await page.locator('#modal .modal-footer button[data-dismiss="modal"]').click();
  });

  await test.step("group modal and DataTables interaction remain functional", async () => {
    await page.getByRole("link", { name: "Users & Groups" }).click();
    await expect(page).toHaveURL(/\/groups$/);
    await expect(page.getByRole("heading", { name: "Users & Groups" })).toBeVisible();
    await expect(page.locator("#groupTable")).toBeVisible();
    await expect(page.locator("#groupTable")).toContainText("Browser Fixture Group");

    await page.getByRole("button", { name: "New Group" }).click();
    await expect(page.locator("#modal")).toBeVisible();
    await page.locator("#firstName").fill("Synthetic");
    await page.locator("#lastName").fill("Browser");
    await page.locator("#email").fill("second-fixture@localhost.invalid");
    await page.locator("#position").fill("Test fixture");
    await page.locator("#targetForm").getByRole("button", { name: "Add" }).click();
    await expect(page.locator("#targetsTable")).toContainText(
      "second-fixture@localhost.invalid",
    );
    await page.locator('#modal .modal-footer button[data-dismiss="modal"]').click();
  });

  await test.step("template editor initializes CKEditor and Bootstrap tabs", async () => {
    await page.getByRole("link", { name: "Email Templates" }).click();
    await expect(page).toHaveURL(/\/templates$/);
    await expect(page.getByRole("heading", { name: "Email Templates" })).toBeVisible();
    await expect(page.locator("#templateTable")).toBeVisible();
    await expect(page.locator("#templateTable")).toContainText(
      "Browser Fixture Template",
    );

    await page.getByRole("button", { name: "New Template" }).click();
    await expect(page.locator("#modal")).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(
          () =>
            typeof window.CKEDITOR === "object" &&
            window.CKEDITOR.instances.html_editor !== undefined,
        ),
      )
      .toBe(true);
    await page.locator('a[href="#html"]').click();
    await expect(page.locator("#html")).toHaveClass(/active/);
    await expect(page.locator(".cke")).toBeVisible();
    await page.locator('#modal .modal-footer button[data-dismiss="modal"]').click();
    await expect(page.locator("#modal")).not.toBeVisible();
  });

  await test.step("password strength bundle resolves zxcvbn", async () => {
    await page.getByRole("link", { name: "User Management" }).click();
    await expect(page).toHaveURL(/\/users$/);
    await expect(page.getByRole("heading", { name: "User Management" })).toBeVisible();

    await page.getByRole("button", { name: "New User" }).click();
    await expect(page.locator("#modal")).toBeVisible();
    await page.locator("#password").fill("browser fixture password 2026");
    await expect(page.locator("#password-strength-container")).not.toHaveClass(
      /hidden/,
    );
    await expect(page.locator("#password-strength-description")).not.toBeEmpty();
  });

  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);
  expect(failedLocalRequests).toEqual([]);
  expect(failedLocalResponses).toEqual([]);
  expect(unexpectedExternalRequests).toEqual([]);
  expect([...loadedAssets]).toEqual(
    expect.arrayContaining([
      "/css/dist/gophish.css",
      "/js/dist/vendor.min.js",
      "/js/dist/app/gophish.min.js",
      "/js/dist/app/dashboard.min.js",
      "/js/dist/app/campaigns.min.js",
      "/js/dist/app/groups.min.js",
      "/js/dist/app/templates.min.js",
      "/js/dist/app/passwords.min.js",
      "/js/dist/app/users.min.js",
      "/js/src/vendor/ckeditor/ckeditor.js",
      "/js/src/vendor/ckeditor/adapters/jquery.js",
    ]),
  );
});
