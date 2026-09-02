import { expect, type Frame, type Page, type Route, test } from "@playwright/test";

type BrowserTelemetry = {
  consoleErrors: string[];
  failedLocalRequests: string[];
  failedLocalResponses: string[];
  pageErrors: string[];
};

type FlashEvent = {
  kind: "error" | "success";
  message: string;
};

type SettingsRequest = {
  accept: string | undefined;
  body: string | null;
  contentType: string | undefined;
  cookiePresent: boolean;
  method: string;
  path: string;
  query: string;
  secFetchSite: string | undefined;
  xRequestedWith: string | undefined;
};

type SettingsContractWindow = Window & {
  __settingsFlashEvents?: FlashEvent[];
  __settingsOriginalErrorFlash?: typeof errorFlash;
  __settingsOriginalSuccessFlash?: typeof successFlash;
};

const encodedUsername = `Équipe sécurité + test & = "quote" 'apostrophe' ~`;
const syntheticCurrentPassword = "synthetic current + & =";
const syntheticNewPassword = `synthetic new + & = "quote" 'apostrophe' ~`;
const syntheticCSRFToken = "synthetic csrf + & =";

function formEncode(entries: Array<[string, string]>): string {
  return entries
    .map(
      ([name, value]) =>
        `${encodeURIComponent(name)}=${encodeURIComponent(value)}`,
    )
    .join("&")
    .replace(/%20/g, "+");
}

function isSettingsPOST(route: Route): boolean {
  const request = route.request();
  return request.method() === "POST" && new URL(request.url()).pathname === "/settings";
}

async function recordSettingsRequest(route: Route): Promise<SettingsRequest> {
  const request = route.request();
  const headers = await request.allHeaders();
  const url = new URL(request.url());
  return {
    accept: headers.accept,
    body: request.postData(),
    contentType: headers["content-type"],
    cookiePresent: Boolean(headers.cookie),
    method: request.method(),
    path: url.pathname,
    query: url.search,
    secFetchSite: headers["sec-fetch-site"],
    xRequestedWith: headers["x-requested-with"],
  };
}

async function installFlashRecorder(page: Page): Promise<void> {
  await page.evaluate(() => {
    const scope = window as SettingsContractWindow;
    scope.__settingsFlashEvents = [];
    scope.__settingsOriginalErrorFlash = errorFlash;
    scope.__settingsOriginalSuccessFlash = successFlash;
    errorFlash = (message: string) => {
      scope.__settingsFlashEvents?.push({
        kind: "error",
        message: String(message),
      });
    };
    successFlash = (message: string) => {
      scope.__settingsFlashEvents?.push({
        kind: "success",
        message: String(message),
      });
    };
  });
}

async function readFlashEvents(page: Page): Promise<FlashEvent[]> {
  return page.evaluate(
    () => (window as SettingsContractWindow).__settingsFlashEvents ?? [],
  );
}

async function clearFlashEvents(page: Page): Promise<void> {
  await page.evaluate(() => {
    (window as SettingsContractWindow).__settingsFlashEvents = [];
  });
}

async function restoreFlashFunctions(page: Page): Promise<void> {
  await page.evaluate(() => {
    const scope = window as SettingsContractWindow;
    if (scope.__settingsOriginalErrorFlash) {
      errorFlash = scope.__settingsOriginalErrorFlash;
    }
    if (scope.__settingsOriginalSuccessFlash) {
      successFlash = scope.__settingsOriginalSuccessFlash;
    }
    delete scope.__settingsFlashEvents;
    delete scope.__settingsOriginalErrorFlash;
    delete scope.__settingsOriginalSuccessFlash;
  });
}

async function submitSettingsForm(page: Page): Promise<void> {
  await page.locator("#settingsForm button[type=submit]").click();
}

async function assertControlInventory(page: Page): Promise<void> {
  const controls = await page.locator("#settingsForm").evaluate((form) =>
    Array.from((form as HTMLFormElement).elements).map((element) => {
      const control = element as HTMLInputElement | HTMLButtonElement;
      return {
        checked: "checked" in control ? control.checked : null,
        disabled: control.disabled,
        multiple: "multiple" in control ? control.multiple : null,
        name: control.name,
        tag: control.tagName.toLowerCase(),
        type: control.type,
      };
    }),
  );

  expect(controls).toEqual([
    {
      checked: false,
      disabled: false,
      multiple: false,
      name: "username",
      tag: "input",
      type: "text",
    },
    {
      checked: false,
      disabled: false,
      multiple: false,
      name: "current_password",
      tag: "input",
      type: "password",
    },
    {
      checked: false,
      disabled: false,
      multiple: false,
      name: "new_password",
      tag: "input",
      type: "password",
    },
    {
      checked: false,
      disabled: false,
      multiple: false,
      name: "confirm_new_password",
      tag: "input",
      type: "password",
    },
    {
      checked: false,
      disabled: false,
      multiple: false,
      name: "csrf_token",
      tag: "input",
      type: "hidden",
    },
    {
      checked: null,
      disabled: false,
      multiple: null,
      name: "",
      tag: "button",
      type: "submit",
    },
  ]);
}

async function assertEncodingAndTransport(
  page: Page,
  nativeTransport: boolean,
): Promise<void> {
  await page.locator('#settingsForm input[name="username"]').fill(encodedUsername);
  await page
    .locator('#settingsForm input[name="current_password"]')
    .fill(syntheticCurrentPassword);
  await page
    .locator('#settingsForm input[name="new_password"]')
    .fill(syntheticNewPassword);
  await page
    .locator('#settingsForm input[name="confirm_new_password"]')
    .fill(syntheticNewPassword);
  await page
    .locator('#settingsForm input[name="csrf_token"]')
    .evaluate((input, value) => {
      (input as HTMLInputElement).value = value;
    }, syntheticCSRFToken);

  const requests: SettingsRequest[] = [];
  await page.route("**/settings", async (route) => {
    if (!isSettingsPOST(route)) {
      await route.continue();
      return;
    }
    requests.push(await recordSettingsRequest(route));
    await route.fulfill({
      body: JSON.stringify({ message: "Settings updated" }),
      contentType: "application/json",
      status: 200,
    });
  });

  const navigations: string[] = [];
  const recordNavigation = (frame: Frame) => {
    if (frame === page.mainFrame()) {
      navigations.push(frame.url());
    }
  };
  page.on("framenavigated", recordNavigation);
  await submitSettingsForm(page);
  await expect.poll(() => requests.length).toBe(1);
  await expect.poll(async () => readFlashEvents(page)).toEqual([
    { kind: "success", message: "Settings updated" },
  ]);
  page.off("framenavigated", recordNavigation);
  await page.unroute("**/settings");

  const expectedBody = formEncode([
    ["username", encodedUsername],
    ["current_password", syntheticCurrentPassword],
    ["new_password", syntheticNewPassword],
    ["confirm_new_password", syntheticNewPassword],
    ["csrf_token", syntheticCSRFToken],
  ]);
  expect(requests).toEqual([
    {
      accept: "*/*",
      body: expectedBody,
      contentType: "application/x-www-form-urlencoded; charset=UTF-8",
      cookiePresent: true,
      method: "POST",
      path: "/settings",
      query: "",
      secFetchSite: undefined,
      xRequestedWith: nativeTransport ? undefined : "XMLHttpRequest",
    },
  ]);
  expect(expectedBody).toContain(
    "username=%C3%89quipe+s%C3%A9curit%C3%A9+%2B+test+%26+%3D+%22quote%22+'apostrophe'+~",
  );
  expect(expectedBody).not.toContain("%20");
  expect(navigations).toEqual([]);
}

async function assertSuccessfulControls(page: Page): Promise<void> {
  await clearFlashEvents(page);
  await page.locator('#settingsForm input[name="username"]').fill("");
  await page.locator('#settingsForm input[name="current_password"]').fill("synthetic");
  await page.locator('#settingsForm input[name="new_password"]').fill("");
  await page.locator('#settingsForm input[name="confirm_new_password"]').fill("");
  await page
    .locator('#settingsForm input[name="confirm_new_password"]')
    .evaluate((input) => {
      (input as HTMLInputElement).disabled = true;
    });
  await page
    .locator('#settingsForm input[name="csrf_token"]')
    .evaluate((input) => {
      (input as HTMLInputElement).value = "";
    });

  const bodies: Array<string | null> = [];
  await page.route("**/settings", async (route) => {
    if (!isSettingsPOST(route)) {
      await route.continue();
      return;
    }
    bodies.push(route.request().postData());
    await route.fulfill({
      body: JSON.stringify({ message: "Empty settings accepted" }),
      contentType: "application/json",
      status: 200,
    });
  });
  await submitSettingsForm(page);
  await expect.poll(() => bodies.length).toBe(1);
  await page.unroute("**/settings");

  expect(bodies).toEqual([
    "username=&current_password=synthetic&new_password=&csrf_token=",
  ]);
  expect(bodies[0]).not.toContain("confirm_new_password");
  await page
    .locator('#settingsForm input[name="confirm_new_password"]')
    .evaluate((input) => {
      (input as HTMLInputElement).disabled = false;
    });
}

async function assertFailureAndRetry(
  page: Page,
  telemetry: BrowserTelemetry,
): Promise<void> {
  await clearFlashEvents(page);
  const consoleErrorBaseline = telemetry.consoleErrors.length;
  const failedResponseBaseline = telemetry.failedLocalResponses.length;
  const failures = [
    { message: "Synthetic bad request", status: 400 },
    { message: "Synthetic CSRF failure", status: 403 },
    { message: "Synthetic server failure", status: 500 },
  ];
  let attempts = 0;
  await page.route("**/settings", async (route) => {
    if (!isSettingsPOST(route)) {
      await route.continue();
      return;
    }
    attempts += 1;
    const failure = failures[attempts - 1];
    await route.fulfill({
      body: JSON.stringify({
        message: failure?.message ?? "Synthetic retry success",
      }),
      contentType: "application/json",
      status: failure?.status ?? 200,
    });
  });

  for (const [index, failure] of failures.entries()) {
    await submitSettingsForm(page);
    await expect.poll(async () => readFlashEvents(page)).toEqual(
      failures.slice(0, index + 1).map(({ message }) => ({
        kind: "error",
        message,
      })),
    );
    await expect(
      page.locator('#settingsForm input[name="current_password"]'),
    ).toHaveValue("synthetic");
  }

  await submitSettingsForm(page);
  await expect.poll(async () => readFlashEvents(page)).toEqual([
    ...failures.map(({ message }) => ({ kind: "error" as const, message })),
    { kind: "success", message: "Synthetic retry success" },
  ]);
  expect(attempts).toBe(4);
  await page.unroute("**/settings");

  await expect
    .poll(() => telemetry.failedLocalResponses.length)
    .toBe(failedResponseBaseline + failures.length);
  expect(telemetry.failedLocalResponses.slice(failedResponseBaseline)).toEqual([
    "400 /settings",
    "403 /settings",
    "500 /settings",
  ]);
  telemetry.failedLocalResponses.length = failedResponseBaseline;
  await expect.poll(() => telemetry.consoleErrors.length).toBeGreaterThan(
    consoleErrorBaseline,
  );
  expect(
    telemetry.consoleErrors
      .slice(consoleErrorBaseline)
      .filter(
        (entry) =>
          !/status of (400 \(Bad Request\)|403 \(Forbidden\)|500 \(Internal Server Error\))/.test(
            entry,
          ),
      ),
  ).toEqual([]);
  telemetry.consoleErrors.length = consoleErrorBaseline;
}

async function assertDoubleSubmit(page: Page): Promise<void> {
  await clearFlashEvents(page);
  let requests = 0;
  await page.route("**/settings", async (route) => {
    if (!isSettingsPOST(route)) {
      await route.continue();
      return;
    }
    requests += 1;
    await route.fulfill({
      body: JSON.stringify({ message: `Synthetic save ${requests}` }),
      contentType: "application/json",
      status: 200,
    });
  });

  await page.locator("#settingsForm button[type=submit]").evaluate((button) => {
    (button as HTMLButtonElement).click();
    (button as HTMLButtonElement).click();
  });
  await expect.poll(() => requests).toBe(2);
  await expect.poll(async () => readFlashEvents(page)).toEqual([
    { kind: "success", message: "Synthetic save 1" },
    { kind: "success", message: "Synthetic save 2" },
  ]);
  await page.unroute("**/settings");
}

async function assertRedirectContract(page: Page): Promise<void> {
  await clearFlashEvents(page);
  await page.route("**/settings", async (route) => {
    if (!isSettingsPOST(route)) {
      await route.continue();
      return;
    }
    await route.fulfill({
      headers: { location: "/settings" },
      status: 302,
    });
  });

  const currentURL = page.url();
  await submitSettingsForm(page);
  await expect.poll(async () => readFlashEvents(page)).toEqual([
    { kind: "success", message: "undefined" },
  ]);
  expect(page.url()).toBe(currentURL);
  await page.unroute("**/settings");
}

async function assertNativeWithoutJQuery(page: Page): Promise<void> {
  await clearFlashEvents(page);
  await page.route("**/settings", async (route) => {
    if (!isSettingsPOST(route)) {
      await route.continue();
      return;
    }
    await route.fulfill({
      body: JSON.stringify({ message: "Native settings saved" }),
      contentType: "application/json",
      status: 200,
    });
  });

  await page.evaluate(() => {
    const scope = window as Window & {
      __settingsOriginalDollar?: typeof window.$;
      __settingsOriginalJQuery?: typeof window.jQuery;
    };
    scope.__settingsOriginalDollar = window.$;
    scope.__settingsOriginalJQuery = window.jQuery;
    window.$ = undefined;
    window.jQuery = undefined;
    document.querySelector<HTMLButtonElement>(
      "#settingsForm button[type=submit]",
    )?.click();
  });
  await expect.poll(async () => readFlashEvents(page)).toEqual([
    { kind: "success", message: "Native settings saved" },
  ]);
  await page.evaluate(() => {
    const scope = window as Window & {
      __settingsOriginalDollar?: typeof window.$;
      __settingsOriginalJQuery?: typeof window.jQuery;
    };
    window.$ = scope.__settingsOriginalDollar;
    window.jQuery = scope.__settingsOriginalJQuery;
    delete scope.__settingsOriginalDollar;
    delete scope.__settingsOriginalJQuery;
  });
  await page.unroute("**/settings");
}

export async function assertSettingsFormContract(
  page: Page,
  telemetry: BrowserTelemetry,
  nativeTransport: boolean,
): Promise<void> {
  await test.step("settings form exposes the measured control inventory", async () => {
    await assertControlInventory(page);
  });

  await installFlashRecorder(page);
  try {
    await test.step("settings form preserves encoding and HTTP transport", async () => {
      await assertEncodingAndTransport(page, nativeTransport);
    });
    await test.step("settings form submits only successful controls", async () => {
      await assertSuccessfulControls(page);
    });
    await test.step("settings form preserves failure and retry behavior", async () => {
      await assertFailureAndRetry(page, telemetry);
    });
    await test.step("settings form preserves double-submit behavior", async () => {
      await assertDoubleSubmit(page);
    });
    await test.step("settings form preserves redirect behavior", async () => {
      await assertRedirectContract(page);
    });
    if (nativeTransport) {
      await test.step("settings form submits without jQuery", async () => {
        await assertNativeWithoutJQuery(page);
      });
    }
  } finally {
    await restoreFlashFunctions(page);
  }

  expect(telemetry.pageErrors).toEqual([]);
}
