import { expect, test, type Page } from "@playwright/test";

const username = requiredEnvironmentVariable("GOPHISHFR_BROWSER_USERNAME");
const password = requiredEnvironmentVariable("GOPHISHFR_BROWSER_PASSWORD");

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

// Mirrors the equivalent helper in frontend-smoke.spec.ts: the shared #modal
// element needs the Bootstrap instance nudged explicitly in this harness.
async function openWebhookModal(page: Page, triggerName: string): Promise<void> {
  await page.waitForFunction(() => typeof window.bootstrap !== "undefined");
  await page.getByRole("button", { name: triggerName }).click();
  await page.evaluate(() => {
    const modal = document.querySelector("#modal");
    if (modal && !modal.classList.contains("show")) {
      window.bootstrap.Modal.getOrCreateInstance(modal, {
        backdrop: "static",
        keyboard: false,
      }).show();
    }
  });
  await expect(page.locator("#modal")).toBeVisible();
}

type CapturedWebhookRequest = {
  body: string | null;
  method: string;
};

test("webhook secret create, edit, preserve, clear, and replace never expose or leak the secret", async ({
  page,
}) => {
  const secretA = `secret-A-${Date.now()}`;
  const secretB = `secret-B-${Date.now()}`;

  await login(page);

  const consoleMessages: string[] = [];
  page.on("console", (message) => consoleMessages.push(message.text()));

  const responseBodies: string[] = [];
  page.on("response", (response) => {
    const url = new URL(response.url());
    if (!url.pathname.startsWith("/api/webhooks/")) {
      return;
    }
    response
      .text()
      .then((body) => responseBodies.push(body))
      .catch(() => {
        // The body may be unavailable (e.g. a redirect); irrelevant here.
      });
  });

  const requests: CapturedWebhookRequest[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (!url.pathname.startsWith("/api/webhooks/")) {
      return;
    }
    requests.push({ body: request.postData(), method: request.method() });
  });

  await page.goto("/webhooks");
  await page.locator("#webhookTable").waitFor({ state: "visible" });

  // 1. Create with a secret: the request must carry it, and every response
  //    seen so far -- including this create's own response -- must not.
  const webhookName = `browser-secret-webhook-${Date.now()}`;
  await openWebhookModal(page, "New Webhook");
  await expect(page.locator("#secret")).toHaveValue("");
  await expect(page.locator("#clear_secret_row")).toBeHidden();
  await page.locator("#name").fill(webhookName);
  await page.locator("#url").fill("https://webhook.invalid/browser-lifecycle");
  await page.locator("#secret").fill(secretA);
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator('[id="flashes"]').first()).toContainText(
    "has been created successfully",
  );
  await expect(page.locator("#webhookTable")).toContainText(webhookName);

  const createRequest = requests
    .filter((entry) => entry.method === "POST")
    .at(-1);
  expect(
    createRequest?.body ? JSON.parse(createRequest.body).secret : undefined,
  ).toBe(secretA);
  for (const body of responseBodies) {
    expect(body).not.toContain(secretA);
    expect(body).not.toContain("secret_ciphertext");
    expect(body).not.toContain("gophishfr-cred:");
  }

  const row = page.locator("#webhookTable tbody tr").filter({ hasText: webhookName });

  // 2. Edit: the field is never prefilled or fetched, and the explicit
  //    removal control is now offered (a webhook exists to remove from).
  await row.locator("button.edit_button").click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#secret")).toHaveValue("");
  await expect(page.locator("#clear_secret_row")).toBeVisible();
  expect(await page.locator("#clear_secret").isChecked()).toBe(false);

  // 3. Ordinary preserve: change only the name; the request must omit
  //    "secret" entirely rather than resend or clear it.
  const preservedName = `${webhookName}-renamed`;
  await page.locator("#name").fill(preservedName);
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator('[id="flashes"]').first()).toContainText(
    "has been updated successfully",
  );
  const preserveRequest = requests.filter((entry) => entry.method === "PUT").at(-1);
  expect(preserveRequest?.body).toBeTruthy();
  expect(JSON.parse(preserveRequest!.body!)).not.toHaveProperty("secret");

  const renamedRow = page
    .locator("#webhookTable tbody tr")
    .filter({ hasText: preservedName });
  await expect(renamedRow).toBeVisible();

  // 4. Explicit removal and replacement are mutually exclusive: checking the
  //    box then typing a replacement disarms the checkbox, and the request
  //    carries the new value, not an empty string.
  await renamedRow.locator("button.edit_button").click();
  await expect(page.locator("#modal")).toBeVisible();
  await page.locator('label[for="clear_secret"]').click();
  expect(await page.locator("#clear_secret").isChecked()).toBe(true);
  await page.locator("#secret").fill(secretB);
  expect(await page.locator("#clear_secret").isChecked()).toBe(false);
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator('[id="flashes"]').first()).toContainText(
    "has been updated successfully",
  );
  const replaceRequest = requests.filter((entry) => entry.method === "PUT").at(-1);
  expect(JSON.parse(replaceRequest!.body!).secret).toBe(secretB);

  // 5. Typing a replacement then disarms if the box is checked afterward:
  //    checking the box clears whatever was typed, so a deliberate removal
  //    can never be masked by unsaved text.
  await renamedRow.locator("button.edit_button").click();
  await expect(page.locator("#modal")).toBeVisible();
  await page.locator("#secret").fill("should-be-cleared");
  await page.locator('label[for="clear_secret"]').click();
  await expect(page.locator("#secret")).toHaveValue("");
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator('[id="flashes"]').first()).toContainText(
    "has been updated successfully",
  );
  const clearRequest = requests.filter((entry) => entry.method === "PUT").at(-1);
  const clearPayload = JSON.parse(clearRequest!.body!);
  expect(clearPayload).toHaveProperty("secret");
  expect(clearPayload.secret).toBe("");

  // 6. Create without a secret: an untouched blank field omits "secret"
  //    entirely rather than sending an implicit empty value.
  const noSecretName = `browser-no-secret-webhook-${Date.now()}`;
  await openWebhookModal(page, "New Webhook");
  await expect(page.locator("#secret")).toHaveValue("");
  await expect(page.locator("#clear_secret_row")).toBeHidden();
  await page.locator("#name").fill(noSecretName);
  await page.locator("#url").fill("https://webhook.invalid/browser-no-secret");
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator('[id="flashes"]').first()).toContainText(
    "has been created successfully",
  );
  const noSecretRequest = requests.filter((entry) => entry.method === "POST").at(-1);
  expect(JSON.parse(noSecretRequest!.body!)).not.toHaveProperty("secret");

  // 7. Absence from storage, console, and every observed response: neither
  //    secret ever appears anywhere outside the deliberate request bodies
  //    asserted above.
  const storageDump = await page.evaluate(() => ({
    local: JSON.stringify(window.localStorage),
    session: JSON.stringify(window.sessionStorage),
  }));
  for (const secret of [secretA, secretB]) {
    expect(storageDump.local).not.toContain(secret);
    expect(storageDump.session).not.toContain(secret);
    expect(consoleMessages.join("\n")).not.toContain(secret);
  }
  for (const body of responseBodies) {
    expect(body).not.toContain(secretA);
    expect(body).not.toContain(secretB);
    expect(body).not.toContain("secret_ciphertext");
    expect(body).not.toContain("gophishfr-cred:");
  }
});