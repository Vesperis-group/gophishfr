import { expect, test, type Page, type Route } from "@playwright/test";

const username = requiredEnvironmentVariable("GOPHISHFR_BROWSER_USERNAME");
const password = requiredEnvironmentVariable("GOPHISHFR_BROWSER_PASSWORD");

type CapturedRequest = {
  authorizationIsBearer: boolean;
  body: string | null;
  contentType: string | undefined;
  method: string;
  pathname: string;
  search: string;
};

type GroupPayload = {
  id?: number;
  name: string;
  targets: Array<{
    email: string;
    first_name: string;
    last_name: string;
    position: string;
  }>;
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

async function runGroupInvocations(
  page: Page,
  createGroup: GroupPayload,
  updateGroup: GroupPayload & { id: number },
): Promise<unknown[]> {
  return page.evaluate(
    async ({ createPayload, updatePayload }) => {
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
        groupId: {
          delete: (id: number) => Request;
          get: (id: number) => Request;
          put: (group: typeof updatePayload) => Request;
        };
        groups: {
          post: (group: typeof createPayload) => Request;
        };
      };
      const api = (window as unknown as { api: API }).api;
      const requests: Array<[string, Request]> = [
        ["groups.post", api.groups.post(createPayload)],
        ["groupId.get", api.groupId.get(updatePayload.id)],
        ["groupId.put", api.groupId.put(updatePayload)],
        ["groupId.delete", api.groupId.delete(updatePayload.id)],
      ];
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

      return Promise.all(
        requests.map(([name, request]) => settle(name, request)),
      );
    },
    { createPayload: createGroup, updatePayload: updateGroup },
  );
}

async function openGroupModal(page: Page, buttonName: string): Promise<void> {
  await page.getByRole("button", { name: buttonName }).click();
  await expect(page.locator("#modal")).toBeVisible();
}

async function closeGroupModal(page: Page): Promise<void> {
  await page.locator('#modal .modal-footer [data-bs-dismiss="modal"]').click();
  await expect(page.locator("#modal")).toBeHidden();
}

async function addTarget(
  page: Page,
  target: {
    email: string;
    firstName: string;
    lastName: string;
    position: string;
  },
): Promise<void> {
  await page.locator("#firstName").fill(target.firstName);
  await page.locator("#lastName").fill(target.lastName);
  await page.locator("#email").fill(target.email);
  await page.locator("#position").fill(target.position);
  await page.locator("#targetForm").getByRole("button", { name: "Add" }).click();
}

test("group CRUD preserves legacy transport and UI contracts", async ({ page }) => {
  const pageErrors: string[] = [];
  const externalRequests: string[] = [];
  const localOrigin = new URL(
    requiredEnvironmentVariable("GOPHISHFR_BROWSER_BASE_URL"),
  ).origin;

  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("request", (request) => {
    if (new URL(request.url()).origin !== localOrigin) {
      externalRequests.push(request.url());
    }
  });

  await login(page);

  const createGroup: GroupPayload = {
    name: "",
    targets: [
      {
        email: "one@localhost.invalid",
        first_name: "First",
        last_name: "",
        position: "One",
      },
      {
        email: "two@localhost.invalid",
        first_name: "",
        last_name: "Second",
        position: "",
      },
    ],
  };
  const updateGroup = { ...createGroup, id: 108 };
  const captured: CapturedRequest[] = [];
  let failureMode = false;

  await page.route("**/api/groups/**", async (route) => {
    captured.push(captureRequest(route));
    const name = route.request().method();
    await route.fulfill({
      body: JSON.stringify(
        failureMode
          ? { message: "synthetic group wrapper failure" }
          : { name: `synthetic-${name.toLowerCase()}` },
      ),
      contentType: "application/json",
      status: failureMode ? 500 : 200,
    });
  });

  expect(await runGroupInvocations(page, createGroup, updateGroup)).toEqual([
    {
      data: { name: "synthetic-post" },
      name: "groups.post",
      settlement: "success",
    },
    {
      data: { name: "synthetic-get" },
      name: "groupId.get",
      settlement: "success",
    },
    {
      data: { name: "synthetic-put" },
      name: "groupId.put",
      settlement: "success",
    },
    {
      data: { name: "synthetic-delete" },
      name: "groupId.delete",
      settlement: "success",
    },
  ]);
  const expectedRequests: CapturedRequest[] = [
    {
      authorizationIsBearer: true,
      body: JSON.stringify(createGroup),
      contentType: "application/json",
      method: "POST",
      pathname: "/api/groups/",
      search: "",
    },
    {
      authorizationIsBearer: true,
      body: null,
      contentType: "application/json",
      method: "GET",
      pathname: "/api/groups/108",
      search: "?{}",
    },
    {
      authorizationIsBearer: true,
      body: JSON.stringify(updateGroup),
      contentType: "application/json",
      method: "PUT",
      pathname: "/api/groups/108",
      search: "",
    },
    {
      authorizationIsBearer: true,
      body: "{}",
      contentType: "application/json",
      method: "DELETE",
      pathname: "/api/groups/108",
      search: "",
    },
  ];
  expect(captured).toHaveLength(expectedRequests.length);
  expect(captured).toEqual(expect.arrayContaining(expectedRequests));

  captured.length = 0;
  failureMode = true;
  expect(await runGroupInvocations(page, createGroup, updateGroup)).toEqual([
    {
      data: { message: "synthetic group wrapper failure" },
      name: "groups.post",
      settlement: "failure",
      status: 500,
    },
    {
      data: { message: "synthetic group wrapper failure" },
      name: "groupId.get",
      settlement: "failure",
      status: 500,
    },
    {
      data: { message: "synthetic group wrapper failure" },
      name: "groupId.put",
      settlement: "failure",
      status: 500,
    },
    {
      data: { message: "synthetic group wrapper failure" },
      name: "groupId.delete",
      settlement: "failure",
      status: 500,
    },
  ]);
  expect(captured).toHaveLength(4);
  await page.unroute("**/api/groups/**");

  await page.route("**/api/groups/**", (route) => route.abort("failed"));
  expect(await runGroupInvocations(page, createGroup, updateGroup)).toEqual([
    {
      data: undefined,
      name: "groups.post",
      settlement: "failure",
      status: 0,
    },
    {
      data: undefined,
      name: "groupId.get",
      settlement: "failure",
      status: 0,
    },
    {
      data: undefined,
      name: "groupId.put",
      settlement: "failure",
      status: 0,
    },
    {
      data: undefined,
      name: "groupId.delete",
      settlement: "failure",
      status: 0,
    },
  ]);
  await page.unroute("**/api/groups/**");

  await page.route("**/api/groups/**", (route) =>
    route.fulfill({
      body: JSON.stringify({ name: "native-group-request" }),
      contentType: "application/json",
      status: 200,
    }),
  );
  const nativeResults = await page.evaluate(
    async ({ createPayload, updatePayload }) => {
      type NativeRequest = Promise<unknown> & {
        always?: unknown;
        done?: unknown;
        fail?: unknown;
      };
      type API = {
        groupId: {
          delete: (id: number) => NativeRequest;
          get: (id: number) => NativeRequest;
          put: (group: typeof updatePayload) => NativeRequest;
        };
        groups: {
          post: (group: typeof createPayload) => NativeRequest;
        };
      };
      const globals = window as unknown as {
        $?: unknown;
        api: API;
        jQuery?: unknown;
      };
      const originalDollar = globals.$;
      const originalJQuery = globals.jQuery;

      try {
        globals.$ = undefined;
        globals.jQuery = undefined;
        const requests: Array<[string, NativeRequest]> = [
          ["groups.post", globals.api.groups.post(createPayload)],
          ["groupId.get", globals.api.groupId.get(updatePayload.id)],
          ["groupId.put", globals.api.groupId.put(updatePayload)],
          ["groupId.delete", globals.api.groupId.delete(updatePayload.id)],
        ];
        return Promise.all(
          requests.map(async ([name, request]) => ({
            data: await request,
            hasAlways: typeof request.always === "function",
            hasDone: typeof request.done === "function",
            hasFail: typeof request.fail === "function",
            name,
          })),
        );
      } finally {
        globals.$ = originalDollar;
        globals.jQuery = originalJQuery;
      }
    },
    { createPayload: createGroup, updatePayload: updateGroup },
  );
  expect(nativeResults).toEqual(
    ["groups.post", "groupId.get", "groupId.put", "groupId.delete"].map((name) => ({
      data: { name: "native-group-request" },
      hasAlways: false,
      hasDone: false,
      hasFail: false,
      name,
    })),
  );
  await page.unroute("**/api/groups/**");

  await page.goto("/groups");
  await expect(page.locator("#groupTable")).toBeVisible();

  const parentReturns = await page.evaluate(async () => {
    type PendingRequest = {
      done: () => PendingRequest;
      fail: () => PendingRequest;
      then: () => PendingRequest;
    };
    type API = {
      groupId: { get: () => PendingRequest };
      groups: { post: () => PendingRequest };
    };
    const globals = window as unknown as {
      Swal: { fire: () => Promise<{ value: boolean }> };
      api: API;
    };
    const originalGet = globals.api.groupId.get;
    const originalPost = globals.api.groups.post;
    const originalFire = globals.Swal.fire;
    const pending: PendingRequest = {
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
      globals.api.groups.post = () => pending;
      globals.api.groupId.get = () => pending;
      globals.Swal.fire = () => new Promise(() => {});
      window.eval('groups = [{ id: 108, name: "synthetic-return-group" }]');
      window.eval("edit(-1)");
      const saveReturn = window.eval("save(-1)");
      const editReturn = window.eval("edit(108)");
      const deleteReturn = window.eval("deleteGroup(108)");
      return {
        deleteIsUndefined: deleteReturn === undefined,
        editIsUndefined: editReturn === undefined,
        saveIsUndefined: saveReturn === undefined,
      };
    } finally {
      globals.api.groupId.get = originalGet;
      globals.api.groups.post = originalPost;
      globals.Swal.fire = originalFire;
    }
  });
  expect(parentReturns).toEqual({
    deleteIsUndefined: true,
    editIsUndefined: true,
    saveIsUndefined: true,
  });

  await page.reload();
  await expect(page.locator("#groupTable")).toBeVisible();

  const groupRequests: CapturedRequest[] = [];
  const captureGroupRequest = (request: {
    headers: () => Record<string, string>;
    method: () => string;
    postData: () => string | null;
    url: () => string;
  }) => {
    const url = new URL(request.url());
    if (
      (request.method() === "POST" && url.pathname === "/api/groups/") ||
      (["GET", "PUT", "DELETE"].includes(request.method()) &&
        /^\/api\/groups\/\d+$/.test(url.pathname))
    ) {
      const headers = request.headers();
      groupRequests.push({
        authorizationIsBearer: headers.authorization?.startsWith("Bearer ") ?? false,
        body: request.postData(),
        contentType: headers["content-type"],
        method: request.method(),
        pathname: url.pathname,
        search: url.search,
      });
    }
  };
  page.on("request", captureGroupRequest);

  await openGroupModal(page, "New Group");
  await closeGroupModal(page);
  await openGroupModal(page, "New Group");
  const groupName = `GroupCrudBaseline_${Date.now()}`;
  const groupTargets = [
    {
      email: "alpha@localhost.invalid",
      firstName: "Alpha",
      lastName: "One",
      position: "Primary",
    },
    {
      email: "bravo@localhost.invalid",
      firstName: "",
      lastName: "Two",
      position: "",
    },
    {
      email: "charlie@localhost.invalid",
      firstName: "Charlie",
      lastName: "",
      position: "Third",
    },
  ];
  const populateGroup = async () => {
    await page.locator("#name").fill(groupName);
    for (const target of groupTargets) {
      await addTarget(page, target);
    }
  };
  await populateGroup();

  let releaseSupersededCreate: () => void = () => {};
  const supersededCreateGate = new Promise<void>((resolve) => {
    releaseSupersededCreate = resolve;
  });
  await page.route(
    "**/api/groups/",
    async (route) => {
      await supersededCreateGate;
      await route.fulfill({
        body: JSON.stringify({ message: "superseded group create failure" }),
        contentType: "application/json",
        status: 500,
      });
    },
    { times: 1 },
  );
  await page.evaluate(() => {
    const submit = document.querySelector<HTMLButtonElement>("#modalSubmit");
    submit?.click();
    submit?.click();
  });
  await expect
    .poll(() => groupRequests.filter((request) => request.method === "POST").length)
    .toBe(1);
  await expect(page.locator("#modalSubmit")).toBeDisabled();
  await closeGroupModal(page);
  await openGroupModal(page, "New Group");
  await expect(page.locator("#modalSubmit")).toBeDisabled();
  await page.evaluate(() => {
    document.querySelector<HTMLButtonElement>("#modalSubmit")?.click();
  });
  expect(groupRequests.filter((request) => request.method === "POST")).toHaveLength(1);
  releaseSupersededCreate();
  await expect(page.locator("#modalSubmit")).toBeEnabled();
  await expect(page.locator('[id="modal.flashes"]')).not.toContainText(
    "superseded group create failure",
  );

  await populateGroup();
  await page.route(
    "**/api/groups/",
    (route) =>
      route.fulfill({
        body: JSON.stringify({ message: "synthetic group create failure" }),
        contentType: "application/json",
        status: 400,
      }),
    { times: 1 },
  );
  await page.locator("#modalSubmit").click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator('[id="modal.flashes"]')).toContainText(
    "synthetic group create failure",
  );
  await expect(page.locator("#name")).toHaveValue(groupName);
  await expect(page.locator("#targetsTable")).toContainText("alpha@localhost.invalid");
  await expect(page.locator("#targetsTable")).toContainText("bravo@localhost.invalid");
  await expect(page.locator("#targetsTable")).toContainText("charlie@localhost.invalid");
  await expect(page.locator("#modalSubmit")).toBeEnabled();

  await page.locator("#modalSubmit").click();
  await expect(page.locator("#modal")).toBeHidden();
  const createdRow = page
    .locator("#groupTable tbody tr")
    .filter({ hasText: groupName });
  await expect(createdRow).toBeVisible();

  const postRequests = groupRequests.filter((request) => request.method === "POST");
  expect(postRequests).toHaveLength(3);
  expect(postRequests[1].body).toBe(postRequests[0].body);
  expect(postRequests[2].body).toBe(postRequests[0].body);
  const createBody = JSON.parse(postRequests[0].body ?? "") as GroupPayload;
  expect(createBody.name).toBe(groupName);
  expect(createBody.targets).toEqual([
    {
      email: "bravo@localhost.invalid",
      first_name: "",
      last_name: "Two",
      position: "",
    },
    {
      email: "alpha@localhost.invalid",
      first_name: "Alpha",
      last_name: "One",
      position: "Primary",
    },
    {
      email: "charlie@localhost.invalid",
      first_name: "Charlie",
      last_name: "",
      position: "Third",
    },
  ]);

  const deleteHandler =
    (await createdRow.locator("button.btn-danger").getAttribute("onclick")) ?? "";
  const groupIdMatch = deleteHandler.match(/\d+/);
  if (!groupIdMatch) {
    throw new Error(`Unable to determine group id from ${deleteHandler}`);
  }
  const groupId = Number(groupIdMatch[0]);
  const groupDetail = (url: URL) =>
    url.pathname === `/api/groups/${groupId}` && url.search === "?{}";

  let releaseStaleEdit: () => void = () => {};
  const staleEditGate = new Promise<void>((resolve) => {
    releaseStaleEdit = resolve;
  });
  await page.route(
    groupDetail,
    async (route) => {
      await staleEditGate;
      await route.continue();
    },
    { times: 1 },
  );
  await createdRow.locator("button.btn-primary").click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#name")).toHaveValue("");
  await expect(page.locator("#modalSubmit")).toBeDisabled();
  await closeGroupModal(page);

  const fixtureRow = page
    .locator("#groupTable tbody tr")
    .filter({ hasText: "Browser Fixture Group" });
  await fixtureRow.locator("button.btn-primary").click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#name")).toHaveValue("Browser Fixture Group");
  const staleResponse = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return response.request().method() === "GET" && groupDetail(url);
  });
  releaseStaleEdit();
  await staleResponse;
  await expect(page.locator("#name")).toHaveValue("Browser Fixture Group");
  await closeGroupModal(page);

  await page.route(
    groupDetail,
    (route) =>
      route.fulfill({
        body: JSON.stringify({ message: "synthetic group edit failure" }),
        contentType: "application/json",
        status: 404,
      }),
    { times: 1 },
  );
  await createdRow.locator("button.btn-primary").click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator('[id="flashes"]').first()).toContainText(
    "Error fetching group",
  );
  await expect(page.locator("#name")).toHaveValue("");
  await expect(page.locator("#modalSubmit")).toBeDisabled();
  await closeGroupModal(page);

  await createdRow.locator("button.btn-primary").click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#name")).toHaveValue(groupName);
  for (const email of [
    "alpha@localhost.invalid",
    "bravo@localhost.invalid",
    "charlie@localhost.invalid",
  ]) {
    await expect(page.locator("#targetsTable")).toContainText(email);
  }
  await expect(page.locator("#modalSubmit")).toBeEnabled();

  const updatedName = `${groupName}_updated`;
  await page.locator("#name").fill(updatedName);
  let releaseUpdateFailure: () => void = () => {};
  const updateFailureGate = new Promise<void>((resolve) => {
    releaseUpdateFailure = resolve;
  });
  await page.route(
    (url) => url.pathname === `/api/groups/${groupId}`,
    async (route) => {
      await updateFailureGate;
      await route.fulfill({
        body: JSON.stringify({ message: "synthetic group update failure" }),
        contentType: "application/json",
        status: 400,
      });
    },
    { times: 1 },
  );
  await page.evaluate(() => {
    const submit = document.querySelector<HTMLButtonElement>("#modalSubmit");
    submit?.click();
    submit?.click();
  });
  await expect
    .poll(() => groupRequests.filter((request) => request.method === "PUT").length)
    .toBe(1);
  await expect(page.locator("#modalSubmit")).toBeDisabled();
  releaseUpdateFailure();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator('[id="modal.flashes"]')).toContainText(
    "synthetic group update failure",
  );
  await expect(page.locator("#name")).toHaveValue(updatedName);
  await expect(page.locator("#targetsTable")).toContainText("bravo@localhost.invalid");
  await expect(page.locator("#modalSubmit")).toBeEnabled();

  await page.locator("#modalSubmit").click();
  await expect(page.locator("#modal")).toBeHidden();
  const updatedRow = page
    .locator("#groupTable tbody tr")
    .filter({ hasText: updatedName });
  await expect(updatedRow).toBeVisible();

  const putRequests = groupRequests.filter((request) => request.method === "PUT");
  expect(putRequests).toHaveLength(2);
  expect(putRequests[1].body).toBe(putRequests[0].body);
  expect(JSON.parse(putRequests[0].body ?? "")).toEqual({
    id: groupId,
    name: updatedName,
    targets: createBody.targets,
  });

  const deleteCount = () =>
    groupRequests.filter((request) => request.method === "DELETE").length;
  await updatedRow.locator("button.btn-danger").click();
  await expect(page.locator(".swal2-popup")).toContainText("Are you sure?");
  await page.locator(".swal2-cancel").click();
  await expect(page.locator(".swal2-popup")).toBeHidden();
  expect(deleteCount()).toBe(0);
  await expect(updatedRow).toBeVisible();

  let releaseDeleteFailure: () => void = () => {};
  const deleteFailureGate = new Promise<void>((resolve) => {
    releaseDeleteFailure = resolve;
  });
  await page.route(
    (url) => url.pathname === `/api/groups/${groupId}`,
    async (route) => {
      await deleteFailureGate;
      await route.fulfill({
        body: JSON.stringify({ message: "synthetic group delete failure" }),
        contentType: "application/json",
        status: 500,
      });
    },
    { times: 1 },
  );
  await updatedRow.locator("button.btn-danger").click();
  await page.evaluate(() => {
    const confirm = document.querySelector<HTMLButtonElement>(".swal2-confirm");
    confirm?.click();
    confirm?.click();
  });
  await expect.poll(deleteCount).toBe(1);
  releaseDeleteFailure();
  await expect(page.locator(".swal2-popup")).toBeVisible();
  await expect(page.locator(".swal2-validation-message")).toContainText(
    "synthetic group delete failure",
  );
  await expect(updatedRow).toBeVisible();
  await page.evaluate(() => {
    (window as unknown as { Swal: { close: () => void } }).Swal.close();
  });
  await expect(page.locator(".swal2-popup")).toBeHidden();

  await updatedRow.locator("button.btn-danger").click();
  await page.locator(".swal2-confirm").click();
  await expect(page.locator(".swal2-popup")).toContainText("Group Deleted!");
  expect(deleteCount()).toBe(2);
  await Promise.all([
    page.waitForNavigation({ waitUntil: "load" }),
    page.getByRole("button", { name: "OK" }).click(),
  ]);
  await expect(page.locator("#groupTable")).not.toContainText(updatedName);

  expect(
    groupRequests.every(
      (request) =>
        request.authorizationIsBearer && request.contentType === "application/json",
    ),
  ).toBe(true);
  expect(
    groupRequests
      .filter((request) => request.method === "GET")
      .every((request) => request.search === "?{}" && request.body === null),
  ).toBe(true);
  expect(
    groupRequests
      .filter((request) => request.method === "DELETE")
      .every((request) => request.body === "{}"),
  ).toBe(true);
  page.off("request", captureGroupRequest);

  expect(pageErrors).toEqual([]);
  expect(externalRequests).toEqual([]);
});
