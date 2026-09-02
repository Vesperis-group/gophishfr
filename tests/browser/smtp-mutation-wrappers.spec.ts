import { expect, test, type Page, type Route } from "@playwright/test";

const username = requiredEnvironmentVariable("GOPHISHFR_BROWSER_USERNAME");
const password = requiredEnvironmentVariable("GOPHISHFR_BROWSER_PASSWORD");
const localOrigin = new URL(
  requiredEnvironmentVariable("GOPHISHFR_BROWSER_BASE_URL"),
).origin;

type Header = {
  key: string;
  value: string;
};

type SMTPPayload = {
  from_address: string;
  headers: Header[];
  host: string;
  id?: number;
  ignore_cert_errors: boolean;
  interface_type: string;
  name: string;
  password: string;
  username: string;
};

type CapturedRequest = {
  authorizationIsBearer: boolean;
  body: string | null;
  contentType: string | undefined;
  method: string;
  pathname: string;
  search: string;
};

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

function captureRequest(route: Route): CapturedRequest {
  const request = route.request();
  const headers = request.headers();
  const url = new URL(request.url());
  return {
    authorizationIsBearer: headers.authorization?.startsWith("Bearer ") ?? false,
    body: request.postData(),
    contentType: headers["content-type"],
    method: request.method(),
    pathname: url.pathname,
    search: url.search,
  };
}

function mutationRequest(request: {
  headers: () => Record<string, string>;
  method: () => string;
  postData: () => string | null;
  url: () => string;
}): CapturedRequest | undefined {
  const url = new URL(request.url());
  if (
    !(
      (request.method() === "POST" && url.pathname === "/api/smtp/") ||
      (["PUT", "DELETE"].includes(request.method()) &&
        /^\/api\/smtp\/\d+$/.test(url.pathname))
    )
  ) {
    return undefined;
  }
  const headers = request.headers();
  return {
    authorizationIsBearer: headers.authorization?.startsWith("Bearer ") ?? false,
    body: request.postData(),
    contentType: headers["content-type"],
    method: request.method(),
    pathname: url.pathname,
    search: url.search,
  };
}

async function wrappersUseNativeTransport(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const api = (
      window as unknown as {
        api: {
          SMTP: { post: () => unknown };
          SMTPId: { delete: () => unknown; put: () => unknown };
        };
      }
    ).api;
    return [api.SMTP.post, api.SMTPId.put, api.SMTPId.delete].every((wrapper) =>
      wrapper.toString().includes("requestJSON"),
    );
  });
}

async function runWrapperInvocations(
  page: Page,
  createProfile: SMTPPayload,
  updateProfile: SMTPPayload & { id: number },
  withoutJQuery = false,
): Promise<unknown[]> {
  return page.evaluate(
    async ({ createPayload, updatePayload, withoutJQuery }) => {
      type Deferred = {
        done: (callback: (data: unknown) => void) => Deferred;
        fail: (
          callback: (error: {
            data?: unknown;
            responseJSON?: unknown;
            status?: number;
          }) => void,
        ) => Deferred;
      };
      type Request = Deferred | Promise<unknown>;
      type API = {
        SMTP: { post: (profile: typeof createPayload) => Request };
        SMTPId: {
          delete: (id: number) => Request;
          put: (profile: typeof updatePayload) => Request;
        };
      };
      const globals = window as unknown as {
        $?: unknown;
        api: API;
        jQuery?: unknown;
      };
      const originalDollar = globals.$;
      const originalJQuery = globals.jQuery;
      const settle = (name: string, request: Request) => {
        if ("done" in request) {
          return new Promise((resolve) => {
            request
              .done((data) => resolve({ data, name, settlement: "success" }))
              .fail((error) =>
                resolve({
                  data: error.responseJSON,
                  name,
                  settlement: "failure",
                  status: error.status,
                }),
              );
          });
        }
        return Promise.resolve(request).then(
          (data) => ({ data, name, settlement: "success" }),
          (error: { data?: unknown; status?: number }) => ({
            data: error.data,
            name,
            settlement: "failure",
            status: error.status,
          }),
        );
      };

      try {
        if (withoutJQuery) {
          globals.$ = undefined;
          globals.jQuery = undefined;
        }
        const invocations: Array<[string, Request]> = [
          ["SMTP.post", globals.api.SMTP.post(createPayload)],
          ["SMTPId.put", globals.api.SMTPId.put(updatePayload)],
          ["SMTPId.delete", globals.api.SMTPId.delete(updatePayload.id)],
        ];
        return Promise.all(
          invocations.map(([name, request]) => settle(name, request)),
        );
      } finally {
        globals.$ = originalDollar;
        globals.jQuery = originalJQuery;
      }
    },
    {
      createPayload: createProfile,
      updatePayload: updateProfile,
      withoutJQuery,
    },
  );
}

async function openProfileModal(page: Page, name: string): Promise<void> {
  await page.getByRole("button", { name }).click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#headersTable_wrapper")).toBeVisible();
}

async function closeProfileModal(page: Page): Promise<void> {
  await page.locator('#modal .modal-footer [data-bs-dismiss="modal"]').click();
  await expect(page.locator("#modal")).toBeHidden();
}

async function addHeader(page: Page, key: string, value: string): Promise<void> {
  await page.locator("#headerKey").fill(key);
  await page.locator("#headerValue").fill(value);
  await page.locator("#addCustomHeader").click();
}

async function headerRows(page: Page): Promise<Header[]> {
  return page.locator("#headersTable tbody tr").evaluateAll((rows) =>
    rows
      .filter((row) => row.querySelectorAll("td").length > 1)
      .map((row) => {
        const cells = row.querySelectorAll("td");
        return {
          key: cells[0]?.textContent ?? "",
          value: cells[1]?.textContent ?? "",
        };
      }),
  );
}

async function fillProfile(
  page: Page,
  profile: Omit<SMTPPayload, "headers" | "interface_type">,
): Promise<void> {
  await page.locator("#name").fill(profile.name);
  await page.locator("#from").fill(profile.from_address);
  await page.locator("#host").fill(profile.host);
  await page.locator("#username").fill(profile.username);
  await page.locator("#password").fill(profile.password);
  if (profile.ignore_cert_errors) {
    await page.locator("#ignore_cert_errors").check({ force: true });
  } else {
    await page.locator("#ignore_cert_errors").uncheck({ force: true });
  }
}

test("SMTP mutations preserve credential and modal contracts", async ({ page }) => {
  test.setTimeout(120_000);
  const pageErrors: string[] = [];
  const externalRequests: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("request", (request) => {
    if (new URL(request.url()).origin !== localOrigin) {
      externalRequests.push(request.url());
    }
  });

  await login(page);

  const wrapperCreate: SMTPPayload = {
    from_address: "sender@profile.invalid",
    headers: [{ key: "X-Synthetic", value: "wrapper" }],
    host: "smtp.profile.invalid:2525",
    ignore_cert_errors: false,
    interface_type: "SMTP",
    name: "",
    password: "",
    username: "",
  };
  const wrapperUpdate = { ...wrapperCreate, id: 208 };
  const wrapperRequests: CapturedRequest[] = [];
  let wrapperFailure = false;
  const wrapperRoute = async (route: Route) => {
    wrapperRequests.push(captureRequest(route));
    await route.fulfill({
      body: JSON.stringify(
        wrapperFailure
          ? { message: "synthetic SMTP wrapper failure" }
          : { success: true },
      ),
      contentType: "application/json",
      status: wrapperFailure ? 500 : 200,
    });
  };
  await page.route("**/api/smtp/**", wrapperRoute);
  const nativeWrappers = await wrappersUseNativeTransport(page);
  expect(nativeWrappers).toBe(true);

  expect(
    await runWrapperInvocations(page, wrapperCreate, wrapperUpdate, true),
  ).toEqual(
    ["SMTP.post", "SMTPId.put", "SMTPId.delete"].map((name) => ({
      data: { success: true },
      name,
      settlement: "success",
    })),
  );
  expect(wrapperRequests).toEqual([
    {
      authorizationIsBearer: true,
      body: JSON.stringify(wrapperCreate),
      contentType: "application/json",
      method: "POST",
      pathname: "/api/smtp/",
      search: "",
    },
    {
      authorizationIsBearer: true,
      body: JSON.stringify(wrapperUpdate),
      contentType: "application/json",
      method: "PUT",
      pathname: "/api/smtp/208",
      search: "",
    },
    {
      authorizationIsBearer: true,
      body: "{}",
      contentType: "application/json",
      method: "DELETE",
      pathname: "/api/smtp/208",
      search: "",
    },
  ]);

  wrapperRequests.length = 0;
  wrapperFailure = true;
  expect(
    await runWrapperInvocations(page, wrapperCreate, wrapperUpdate),
  ).toEqual(
    ["SMTP.post", "SMTPId.put", "SMTPId.delete"].map((name) => ({
      data: { message: "synthetic SMTP wrapper failure" },
      name,
      settlement: "failure",
      status: 500,
    })),
  );
  expect(wrapperRequests).toHaveLength(3);
  await page.unroute("**/api/smtp/**", wrapperRoute);

  await page.route("**/api/smtp/**", (route) => route.abort("failed"));
  expect(
    await runWrapperInvocations(page, wrapperCreate, wrapperUpdate),
  ).toEqual(
    ["SMTP.post", "SMTPId.put", "SMTPId.delete"].map((name) => ({
      data: undefined,
      name,
      settlement: "failure",
      status: 0,
    })),
  );
  await page.unroute("**/api/smtp/**");

  await page.goto("/sending_profiles");
  await expect(page.locator("#profileTable")).toBeVisible();
  await openProfileModal(page, "New Profile");
  const parentReturns = await page.evaluate(() => {
    type Pending = {
      done: () => Pending;
      fail: () => Pending;
      then: () => Pending;
    };
    type API = {
      SMTP: { post: () => Pending };
      SMTPId: { delete: () => Pending };
    };
    const globals = window as unknown as {
      Swal: { fire: () => Promise<never> };
      api: API;
    };
    const originalPost = globals.api.SMTP.post;
    const originalDelete = globals.api.SMTPId.delete;
    const originalFire = globals.Swal.fire;
    const pending: Pending = {
      done() {
        return pending;
      },
      fail() {
        return pending;
      },
      then() {
        return pending;
      },
    };

    try {
      globals.api.SMTP.post = () => pending;
      globals.api.SMTPId.delete = () => pending;
      globals.Swal.fire = () => new Promise(() => {});
      window.eval(
        'profiles = [{ id: 208, name: "Synthetic profile", headers: [] }]',
      );
      return {
        deleteIsUndefined: window.eval("deleteProfile(0)") === undefined,
        saveIsUndefined: window.eval("save(-1)") === undefined,
      };
    } finally {
      globals.api.SMTP.post = originalPost;
      globals.api.SMTPId.delete = originalDelete;
      globals.Swal.fire = originalFire;
    }
  });
  expect(parentReturns).toEqual({
    deleteIsUndefined: true,
    saveIsUndefined: true,
  });
  await page.reload();
  await expect(page.locator("#profileTable")).toBeVisible();

  const capturedMutations: CapturedRequest[] = [];
  const captureMutation = (request: {
    headers: () => Record<string, string>;
    method: () => string;
    postData: () => string | null;
    url: () => string;
  }) => {
    const captured = mutationRequest(request);
    if (captured) {
      capturedMutations.push(captured);
    }
  };
  page.on("request", captureMutation);

  await openProfileModal(page, "New Profile");
  await closeProfileModal(page);
  await openProfileModal(page, "New Profile");

  const profileName = `SMTPMutationBaseline_${Date.now()}`;
  const createProfile = {
    from_address: "sender@profile.invalid",
    host: "127.0.0.1:2525",
    ignore_cert_errors: false,
    name: profileName,
    password: "synthetic-profile-secret-a",
    username: "synthetic-profile-user",
  };
  await fillProfile(page, createProfile);
  await addHeader(page, "X-Synthetic-Mode", "create");
  await addHeader(page, "X-Synthetic-Remove", "removed");
  await addHeader(page, "X-Synthetic-Mode", "create-updated");
  await page
    .locator("#headersTable tbody tr")
    .filter({ hasText: "X-Synthetic-Remove" })
    .locator("span > i.fa-trash-o")
    .click();
  await expect.poll(() => headerRows(page)).toEqual([
    { key: "X-Synthetic-Mode", value: "create-updated" },
  ]);

  await page.route(
    "**/api/smtp/",
    (route) =>
      route.fulfill({
        body: JSON.stringify({ message: "synthetic profile create failure" }),
        contentType: "application/json",
        status: 400,
      }),
    { times: 1 },
  );
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator('[id="modal.flashes"]')).toContainText(
    "synthetic profile create failure",
  );
  await expect(page.locator("#name")).toHaveValue(profileName);
  await expect(page.locator("#from")).toHaveValue(createProfile.from_address);
  await expect(page.locator("#host")).toHaveValue(createProfile.host);
  await expect(page.locator("#username")).toHaveValue(createProfile.username);
  await expect(page.locator("#password")).toHaveValue(createProfile.password);
  await expect(page.locator("#ignore_cert_errors")).not.toBeChecked();
  await expect.poll(() => headerRows(page)).toEqual([
    { key: "X-Synthetic-Mode", value: "create-updated" },
  ]);

  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeHidden();
  const createdRow = page
    .locator("#profileTable tbody tr")
    .filter({ hasText: profileName });
  await expect(createdRow).toBeVisible();

  const createRequests = capturedMutations.filter(
    (request) =>
      request.method === "POST" &&
      request.pathname === "/api/smtp/" &&
      (JSON.parse(request.body ?? "{}") as SMTPPayload).name === profileName,
  );
  expect(createRequests).toHaveLength(2);
  expect(createRequests[1].body).toBe(createRequests[0].body);
  expect(JSON.parse(createRequests[0].body ?? "")).toEqual({
    from_address: createProfile.from_address,
    headers: [{ key: "X-Synthetic-Mode", value: "create-updated" }],
    host: createProfile.host,
    ignore_cert_errors: false,
    interface_type: "SMTP",
    name: profileName,
    password: createProfile.password,
    username: createProfile.username,
  });

  const editHandler =
    (await createdRow.locator('button[onclick^="edit("]').getAttribute("onclick")) ??
      "";
  const indexMatch = editHandler.match(/\d+/);
  if (!indexMatch) {
    throw new Error(`Unable to determine profile index from ${editHandler}`);
  }
  const profileIndex = Number(indexMatch[0]);
  const profileId = await page.evaluate(
    (index) =>
      (
        window as unknown as {
          profiles: Array<{ id: number }>;
        }
      ).profiles[index].id,
    profileIndex,
  );

  await createdRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#password")).toHaveValue(createProfile.password);
  const unchangedPasswordName = `${profileName}_unchanged_password`;
  await page.locator("#name").fill(unchangedPasswordName);
  await page.locator("#host").fill("127.0.0.1:2526");
  await page.locator("#ignore_cert_errors").check({ force: true });
  await addHeader(page, "X-Synthetic-Update", "unchanged-password");

  await page.route(
    (url) => url.pathname === `/api/smtp/${profileId}`,
    (route) =>
      route.fulfill({
        body: JSON.stringify({ message: "synthetic profile update failure" }),
        contentType: "application/json",
        status: 500,
      }),
    { times: 1 },
  );
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator('[id="modal.flashes"]')).toContainText(
    "synthetic profile update failure",
  );
  await expect(page.locator("#password")).toHaveValue(createProfile.password);
  await expect(page.locator("#modal #modalSubmit")).toBeEnabled();

  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeHidden();
  const unchangedPasswordRow = page
    .locator("#profileTable tbody tr")
    .filter({ hasText: unchangedPasswordName });
  await expect(unchangedPasswordRow).toBeVisible();

  const unchangedPasswordRequests = capturedMutations.filter(
    (request) =>
      request.method === "PUT" &&
      request.pathname === `/api/smtp/${profileId}` &&
      (JSON.parse(request.body ?? "{}") as SMTPPayload).name ===
        unchangedPasswordName,
  );
  expect(unchangedPasswordRequests).toHaveLength(2);
  expect(unchangedPasswordRequests[1].body).toBe(
    unchangedPasswordRequests[0].body,
  );
  expect(JSON.parse(unchangedPasswordRequests[0].body ?? "")).toEqual({
    from_address: createProfile.from_address,
    headers: [
      { key: "X-Synthetic-Mode", value: "create-updated" },
      { key: "X-Synthetic-Update", value: "unchanged-password" },
    ],
    host: "127.0.0.1:2526",
    id: profileId,
    ignore_cert_errors: true,
    interface_type: "SMTP",
    name: unchangedPasswordName,
    password: createProfile.password,
    username: createProfile.username,
  });

  await unchangedPasswordRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#modal")).toBeVisible();
  const changedPasswordName = `${profileName}_changed_password`;
  const changedPassword = "synthetic-profile-secret-b";
  await page.locator("#name").fill(changedPasswordName);
  await page.locator("#password").fill(changedPassword);
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeHidden();
  const changedPasswordRow = page
    .locator("#profileTable tbody tr")
    .filter({ hasText: changedPasswordName });
  await expect(changedPasswordRow).toBeVisible();
  const changedPasswordRequest = capturedMutations.find(
    (request) =>
      request.method === "PUT" &&
      request.pathname === `/api/smtp/${profileId}` &&
      (JSON.parse(request.body ?? "{}") as SMTPPayload).name ===
        changedPasswordName,
  );
  expect(changedPasswordRequest).toBeDefined();
  expect(JSON.parse(changedPasswordRequest?.body ?? "")).toMatchObject({
    id: profileId,
    name: changedPasswordName,
    password: changedPassword,
  });

  await changedPasswordRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#modal")).toBeVisible();
  const emptyPasswordName = `${profileName}_empty_password`;
  await page.locator("#name").fill(emptyPasswordName);
  await page.locator("#password").fill("");
  await page.locator("#username").fill("");
  await page
    .locator("#headersTable tbody tr")
    .locator("span > i.fa-trash-o")
    .first()
    .click();
  await page
    .locator("#headersTable tbody tr")
    .locator("span > i.fa-trash-o")
    .first()
    .click();
  await expect.poll(() => headerRows(page)).toEqual([]);
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeHidden();
  const emptyPasswordRow = page
    .locator("#profileTable tbody tr")
    .filter({ hasText: emptyPasswordName });
  await expect(emptyPasswordRow).toBeVisible();

  const emptyPasswordRequest = capturedMutations.find(
    (request) =>
      request.method === "PUT" &&
      request.pathname === `/api/smtp/${profileId}` &&
      (JSON.parse(request.body ?? "{}") as SMTPPayload).name === emptyPasswordName,
  );
  expect(emptyPasswordRequest).toBeDefined();
  const emptyPasswordBody = JSON.parse(
    emptyPasswordRequest?.body ?? "",
  ) as SMTPPayload;
  expect(Object.hasOwn(emptyPasswordBody, "password")).toBe(true);
  expect(emptyPasswordBody.password).toBe("");
  expect(Object.hasOwn(emptyPasswordBody, "username")).toBe(true);
  expect(emptyPasswordBody.username).toBe("");
  expect(emptyPasswordBody.headers).toEqual([]);

  await emptyPasswordRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#password")).toHaveValue("");
  await expect(page.locator("#username")).toHaveValue("");
  await expect.poll(() => headerRows(page)).toEqual([]);
  await closeProfileModal(page);

  await emptyPasswordRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#name")).toHaveValue(emptyPasswordName);
  await closeProfileModal(page);
  const fixtureRow = page
    .locator("#profileTable tbody tr")
    .filter({ hasText: "Browser Fixture Sending Profile" });
  await fixtureRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#name")).toHaveValue(
    "Browser Fixture Sending Profile",
  );
  await expect(page.locator("#host")).toHaveValue("127.0.0.1:1");
  await closeProfileModal(page);

  const deleteCount = () =>
    capturedMutations.filter(
      (request) =>
        request.method === "DELETE" &&
        request.pathname === `/api/smtp/${profileId}`,
    ).length;
  await emptyPasswordRow.locator("button.btn-danger").click();
  await page.locator(".swal2-cancel").click();
  await expect(page.locator(".swal2-popup")).toBeHidden();
  expect(deleteCount()).toBe(0);
  await expect(emptyPasswordRow).toBeVisible();

  await page.route(
    (url) => url.pathname === `/api/smtp/${profileId}`,
    (route) =>
      route.fulfill({
        body: JSON.stringify({ message: "synthetic profile delete failure" }),
        contentType: "application/json",
        status: 500,
      }),
    { times: 1 },
  );
  await emptyPasswordRow.locator("button.btn-danger").click();
  await page.locator(".swal2-confirm").click();
  await expect.poll(deleteCount).toBe(1);
  await expect(page.locator(".swal2-popup")).toBeVisible();
  await expect(emptyPasswordRow).toBeVisible();

  await page.locator(".swal2-confirm").click();
  await expect(page.locator(".swal2-popup")).toContainText(
    "Sending Profile Deleted!",
  );
  expect(deleteCount()).toBe(2);
  await Promise.all([
    page.waitForNavigation({ waitUntil: "load" }),
    page.getByRole("button", { name: "OK" }).click(),
  ]);
  await expect(page.locator("#profileTable")).not.toContainText(emptyPasswordName);

  expect(
    capturedMutations.every(
      (request) =>
        request.authorizationIsBearer && request.contentType === "application/json",
    ),
  ).toBe(true);
  expect(
    capturedMutations
      .filter((request) => request.method === "DELETE")
      .every((request) => request.body === "{}"),
  ).toBe(true);
  page.off("request", captureMutation);

  expect(await wrappersUseNativeTransport(page)).toBe(true);

  let createCount = 0;
  let releaseCreate: (() => void) | undefined;
  const delayedCreate = async (route: Route) => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    createCount++;
    await new Promise<void>((resolve) => {
      releaseCreate = resolve;
    });
    await route.fulfill({
      body: JSON.stringify({ id: 901, name: "Synthetic delayed profile" }),
      contentType: "application/json",
      status: 201,
    });
  };
  await page.route("**/api/smtp/", delayedCreate);
  await openProfileModal(page, "New Profile");
  await fillProfile(page, {
    from_address: "delayed@profile.invalid",
    host: "127.0.0.1:2525",
    ignore_cert_errors: false,
    name: "Synthetic delayed profile",
    password: "synthetic-delayed-secret",
    username: "synthetic-delayed-user",
  });
  await page.evaluate(() => {
    const globals = window as unknown as { save: (id: number) => void };
    globals.save(-1);
    globals.save(-1);
  });
  await expect.poll(() => createCount).toBe(1);
  await expect(page.locator("#modalSubmit")).toBeDisabled();
  await expect(page.locator("#headersTable_wrapper")).toHaveJSProperty(
    "inert",
    true,
  );

  await closeProfileModal(page);
  await openProfileModal(page, "New Profile");
  await expect(page.locator("#name")).toHaveValue("");
  await expect(page.locator("#modalSubmit")).toBeDisabled();
  if (!releaseCreate) {
    throw new Error("Delayed create request was not captured");
  }
  releaseCreate();
  await expect(page.locator("#modalSubmit")).toBeEnabled();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#name")).toHaveValue("");
  expect(createCount).toBe(1);
  await page.unroute("**/api/smtp/", delayedCreate);
  await closeProfileModal(page);

  const concurrentFixtureRow = page
    .locator("#profileTable tbody tr")
    .filter({ hasText: "Browser Fixture Sending Profile" });
  const fixtureEditHandler =
    (await concurrentFixtureRow
      .locator('button[onclick^="edit("]')
      .getAttribute("onclick")) ?? "";
  const fixtureIndexMatch = fixtureEditHandler.match(/\d+/);
  if (!fixtureIndexMatch) {
    throw new Error(
      `Unable to determine fixture profile index from ${fixtureEditHandler}`,
    );
  }
  const fixtureIndex = Number(fixtureIndexMatch[0]);
  const fixtureId = await page.evaluate(
    (index) =>
      (
        window as unknown as {
          profiles: Array<{ id: number }>;
        }
      ).profiles[index].id,
    fixtureIndex,
  );

  let updateCount = 0;
  let updatePath = "";
  let releaseUpdate: (() => void) | undefined;
  const delayedUpdate = async (route: Route) => {
    updateCount++;
    updatePath = new URL(route.request().url()).pathname;
    await new Promise<void>((resolve) => {
      releaseUpdate = resolve;
    });
    await route.fulfill({
      body: JSON.stringify({ id: fixtureId, name: "Synthetic stale update" }),
      contentType: "application/json",
      status: 200,
    });
  };
  await page.route(`**/api/smtp/${fixtureId}`, delayedUpdate);
  await concurrentFixtureRow.locator('button[onclick^="edit("]').click();
  await page.locator("#name").fill("Synthetic stale update");
  await page.evaluate(() => {
    const globals = window as unknown as {
      profiles: Array<Record<string, unknown>>;
      submitHandler: () => void;
    };
    globals.profiles.unshift({
      headers: [],
      id: 999_999,
      name: "Synthetic reordered profile",
    });
    globals.submitHandler();
    globals.submitHandler();
  });
  await expect.poll(() => updateCount).toBe(1);
  await expect(page.locator("#modalSubmit")).toBeDisabled();

  await closeProfileModal(page);
  await page.evaluate(() => {
    (
      window as unknown as {
        profiles: Array<Record<string, unknown>>;
      }
    ).profiles.shift();
  });
  await concurrentFixtureRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#name")).toHaveValue(
    "Browser Fixture Sending Profile",
  );
  await expect(page.locator("#modalSubmit")).toBeDisabled();
  if (!releaseUpdate) {
    throw new Error("Delayed update request was not captured");
  }
  releaseUpdate();
  await expect(page.locator("#modalSubmit")).toBeEnabled();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#name")).toHaveValue(
    "Browser Fixture Sending Profile",
  );
  expect(updatePath).toBe(`/api/smtp/${fixtureId}`);
  expect(updateCount).toBe(1);
  await page.unroute(`**/api/smtp/${fixtureId}`, delayedUpdate);
  await closeProfileModal(page);

  const refreshedFixtureRow = page
    .locator("#profileTable tbody tr")
    .filter({ hasText: "Browser Fixture Sending Profile" });
  let delayedDeleteCount = 0;
  let deletePath = "";
  let releaseDelete: (() => void) | undefined;
  const delayedDelete = async (route: Route) => {
    delayedDeleteCount++;
    deletePath = new URL(route.request().url()).pathname;
    await new Promise<void>((resolve) => {
      releaseDelete = resolve;
    });
    await route.fulfill({
      body: JSON.stringify({ success: true }),
      contentType: "application/json",
      status: 200,
    });
  };
  await page.route(`**/api/smtp/${fixtureId}`, delayedDelete);
  await refreshedFixtureRow.locator("button.btn-danger").click();
  await page.evaluate(() => {
    (
      window as unknown as {
        profiles: Array<Record<string, unknown>>;
      }
    ).profiles.unshift({
      headers: [],
      id: 999_998,
      name: "Synthetic reordered delete profile",
    });
  });
  const confirmDelete = page.locator(".swal2-confirm");
  await confirmDelete.click();
  await confirmDelete.dispatchEvent("click");
  await expect.poll(() => delayedDeleteCount).toBe(1);
  if (!releaseDelete) {
    throw new Error("Delayed delete request was not captured");
  }
  releaseDelete();
  await expect(page.locator(".swal2-popup")).toContainText(
    "Sending Profile Deleted!",
  );
  expect(deletePath).toBe(`/api/smtp/${fixtureId}`);
  expect(delayedDeleteCount).toBe(1);
  await page.evaluate(() => {
    (window as unknown as { Swal: { close: () => void } }).Swal.close();
  });
  await page.unroute(`**/api/smtp/${fixtureId}`, delayedDelete);

  const relevantPageErrors = pageErrors.filter(
    (message) =>
      !message.includes(
        "Service worker is disabled because the context is sandboxed",
      ),
  );
  expect(relevantPageErrors).toEqual([]);
  expect(externalRequests).toEqual([]);
});
