import { expect, test, type Page, type Route } from "@playwright/test";

const username = requiredEnvironmentVariable("GOPHISHFR_BROWSER_USERNAME");
const password = requiredEnvironmentVariable("GOPHISHFR_BROWSER_PASSWORD");
const localOrigin = new URL(
  requiredEnvironmentVariable("GOPHISHFR_BROWSER_BASE_URL"),
).origin;

type CapturedRequest = {
  authorizationIsBearer: boolean;
  body: string | null;
  contentType: string | undefined;
  method: string;
  pathname: string;
  search: string;
};

type LandingPagePayload = {
  capture_credentials: boolean;
  capture_passwords: boolean;
  html: string;
  id?: number;
  name: string;
  redirect_url: string;
};

const createdHTML =
  '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Landing &amp; exact</title><style>.panel{color:#123456}</style></head><body class="landing-body" data-purpose="round-trip"><main><form class="panel" data-state="created" action="/submit" style="padding: 4px"><label for="account">Account</label><input id="account" name="account" value="{{.FirstName}}" data-note="A &amp; B"><input name="password" type="password"><button type="submit" title="Single \' and &quot;double&quot;">Continue</button></form></main></body></html>';
const updatedHTML =
  '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Updated &amp; exact</title><style>.panel{color:#654321}</style></head><body class="landing-body updated" data-purpose="round-trip" data-state="updated"><section><form class="panel" action="/updated" style="padding: 8px"><input name="username" value="{{.FirstName}} {{.LastName}}"><input name="password" type="password"><button type="submit">Sign in</button></form></section></body></html>';
const canonicalSourceHTML =
  '<!doctype html><html><head><title>Canonical response</title></head><body><form action="/must-be-rewritten"><input name="username"><input name="password" type="password"><button type="submit">Continue</button></form></body></html>';
const clonedHTML =
  '<!doctype html><html><head><title>Remote clone</title></head><body data-clone="synthetic"><article id="synthetic-clone-marker" class="remote" data-source="clone"><img id="clone-image" src="https://external.invalid/pixel.png" onerror="window.__cloneHandlerExecuted=true"><form id="clone-form" action="https://external.invalid/submit"><input name="username"><button type="submit">Submit</button></form><script>window.__cloneScriptExecuted=true</script></article></body></html>';

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

async function wrappersUseNativeTransport(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const api = (
      window as unknown as {
        api: {
          clone_site: () => unknown;
          pageId: { delete: () => unknown; put: () => unknown };
          pages: { post: () => unknown };
        };
      }
    ).api;
    return [
      api.pages.post,
      api.pageId.put,
      api.pageId.delete,
      api.clone_site,
    ].every((wrapper) => wrapper.toString().includes("requestJSON"));
  });
}

async function runWrapperInvocations(
  page: Page,
  createPage: LandingPagePayload,
  updatePage: LandingPagePayload & { id: number },
  cloneRequest: { include_resources: boolean; url: string },
  withoutJQuery = false,
): Promise<unknown[]> {
  return page.evaluate(
    async ({ clonePayload, createPayload, updatePayload, withoutJQuery }) => {
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
        clone_site: (request: typeof clonePayload) => Request;
        pageId: {
          delete: (id: number) => Request;
          put: (page: typeof updatePayload) => Request;
        };
        pages: {
          post: (page: typeof createPayload) => Request;
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
          ["pages.post", globals.api.pages.post(createPayload)],
          ["pageId.put", globals.api.pageId.put(updatePayload)],
          ["pageId.delete", globals.api.pageId.delete(updatePayload.id)],
          ["clone_site", globals.api.clone_site(clonePayload)],
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
      clonePayload: cloneRequest,
      createPayload: createPage,
      updatePayload: updatePage,
      withoutJQuery,
    },
  );
}

async function openPageModal(page: Page, name: string): Promise<void> {
  await page.getByRole("button", { name }).click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          (
            window as unknown as {
              GophishHTMLEditor: { get: (id: string) => unknown };
            }
          ).GophishHTMLEditor.get("html_editor") !== undefined,
      ),
    )
    .toBe(true);
}

async function closePageModal(page: Page): Promise<void> {
  await page.locator('#modal .modal-footer [data-bs-dismiss="modal"]').click();
  await expect(page.locator("#modal")).toBeHidden();
}

async function setHTML(page: Page, html: string): Promise<void> {
  await page.locator(".gophish-code-editor .cm-content").fill(html);
  await expect.poll(() => getHTML(page)).toBe(html);
}

async function getHTML(page: Page): Promise<string | undefined> {
  return page.evaluate(
    () =>
      (
        window as unknown as {
          GophishHTMLEditor: {
            get: (id: string) => { getData: () => string } | undefined;
          };
        }
      ).GophishHTMLEditor.get("html_editor")?.getData(),
  );
}

function mutationRequest(request: {
  headers: () => Record<string, string>;
  method: () => string;
  postData: () => string | null;
  url: () => string;
}): CapturedRequest | undefined {
  const url = new URL(request.url());
  const isPageMutation =
    (request.method() === "POST" && url.pathname === "/api/pages/") ||
    (["PUT", "DELETE"].includes(request.method()) &&
      /^\/api\/pages\/\d+$/.test(url.pathname));
  if (!isPageMutation && url.pathname !== "/api/import/site") {
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

test("landing page mutations and site cloning preserve legacy contracts", async ({
  page,
}) => {
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

  const wrapperCreate: LandingPagePayload = {
    capture_credentials: true,
    capture_passwords: false,
    html: createdHTML,
    name: "",
    redirect_url: "",
  };
  const wrapperUpdate = { ...wrapperCreate, id: 208 };
  const wrapperClone = {
    include_resources: false,
    url: "https://clone.invalid/wrapper?value=1",
  };
  const wrapperRequests: CapturedRequest[] = [];
  let wrapperFailure = false;
  const wrapperRoute = async (route: Route) => {
    wrapperRequests.push(captureRequest(route));
    await route.fulfill({
      body: JSON.stringify(
        wrapperFailure
          ? { message: "synthetic landing page wrapper failure" }
          : { success: true },
      ),
      contentType: "application/json",
      status: wrapperFailure ? 500 : 200,
    });
  };
  await page.route("**/api/pages/**", wrapperRoute);
  await page.route("**/api/import/site", wrapperRoute);
  const nativeWrappers = await wrappersUseNativeTransport(page);
  expect(nativeWrappers).toBe(true);

  expect(
    await runWrapperInvocations(
      page,
      wrapperCreate,
      wrapperUpdate,
      wrapperClone,
      true,
    ),
  ).toEqual(
    ["pages.post", "pageId.put", "pageId.delete", "clone_site"].map((name) => ({
      data: { success: true },
      name,
      settlement: "success",
    })),
  );
  const expectedWrapperRequests: CapturedRequest[] = [
    {
      authorizationIsBearer: true,
      body: JSON.stringify(wrapperCreate),
      contentType: "application/json",
      method: "POST",
      pathname: "/api/pages/",
      search: "",
    },
    {
      authorizationIsBearer: true,
      body: JSON.stringify(wrapperUpdate),
      contentType: "application/json",
      method: "PUT",
      pathname: "/api/pages/208",
      search: "",
    },
    {
      authorizationIsBearer: true,
      body: "{}",
      contentType: "application/json",
      method: "DELETE",
      pathname: "/api/pages/208",
      search: "",
    },
    {
      authorizationIsBearer: true,
      body: JSON.stringify(wrapperClone),
      contentType: "application/json",
      method: "POST",
      pathname: "/api/import/site",
      search: "",
    },
  ];
  expect(wrapperRequests).toEqual(expectedWrapperRequests);

  wrapperRequests.length = 0;
  wrapperFailure = true;
  expect(
    await runWrapperInvocations(
      page,
      wrapperCreate,
      wrapperUpdate,
      wrapperClone,
    ),
  ).toEqual(
    ["pages.post", "pageId.put", "pageId.delete", "clone_site"].map((name) => ({
      data: { message: "synthetic landing page wrapper failure" },
      name,
      settlement: "failure",
      status: 500,
    })),
  );
  expect(wrapperRequests).toHaveLength(4);
  await page.unroute("**/api/pages/**", wrapperRoute);
  await page.unroute("**/api/import/site", wrapperRoute);

  await page.route("**/api/pages/**", (route) => route.abort("failed"));
  await page.route("**/api/import/site", (route) => route.abort("failed"));
  expect(
    await runWrapperInvocations(
      page,
      wrapperCreate,
      wrapperUpdate,
      wrapperClone,
    ),
  ).toEqual(
    ["pages.post", "pageId.put", "pageId.delete", "clone_site"].map((name) => ({
      data: undefined,
      name,
      settlement: "failure",
      status: 0,
    })),
  );
  await page.unroute("**/api/pages/**");
  await page.unroute("**/api/import/site");

  await page.goto("/landing_pages");
  await expect(page.locator("#pagesTable")).toBeVisible();

  await openPageModal(page, "New Page");
  const parentReturns = await page.evaluate(() => {
    type Pending = {
      done: () => Pending;
      fail: () => Pending;
      then: () => Pending;
    };
    type API = {
      clone_site: () => Pending;
      pageId: { delete: () => Pending };
      pages: { post: () => Pending };
    };
    const globals = window as unknown as {
      Swal: { fire: () => Promise<never> };
      api: API;
    };
    const originalClone = globals.api.clone_site;
    const originalPost = globals.api.pages.post;
    const originalDelete = globals.api.pageId.delete;
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
      globals.api.clone_site = () => pending;
      globals.api.pages.post = () => pending;
      globals.api.pageId.delete = () => pending;
      globals.Swal.fire = () => new Promise(() => {});
      window.eval('pages = [{ id: 208, name: "Synthetic landing page" }]');
      (document.getElementById("url") as HTMLInputElement).value =
        "https://clone.invalid/parent";
      return {
        deleteIsUndefined: window.eval("deletePage(0)") === undefined,
        importIsUndefined: window.eval("importSite()") === undefined,
        saveIsUndefined: window.eval("save(-1)") === undefined,
      };
    } finally {
      globals.api.clone_site = originalClone;
      globals.api.pages.post = originalPost;
      globals.api.pageId.delete = originalDelete;
      globals.Swal.fire = originalFire;
    }
  });
  expect(parentReturns).toEqual({
    deleteIsUndefined: true,
    importIsUndefined: true,
    saveIsUndefined: true,
  });
  await page.reload();
  await expect(page.locator("#pagesTable")).toBeVisible();

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

  const supersededName = `SupersededLandingPage_${Date.now()}`;
  let releaseSupersededCreate: () => void = () => {};
  const supersededCreateGate = new Promise<void>((resolve) => {
    releaseSupersededCreate = resolve;
  });
  await page.route(
    "**/api/pages/",
    async (route) => {
      await supersededCreateGate;
      await route.fulfill({
        body: JSON.stringify({
          message: "superseded landing page create failure",
        }),
        contentType: "application/json",
        status: 400,
      });
    },
    { times: 1 },
  );
  await openPageModal(page, "New Page");
  await page.locator("#name").fill(supersededName);
  await setHTML(page, createdHTML);
  await page.evaluate(() => {
    const submit = document.querySelector<HTMLButtonElement>(
      "#modal #modalSubmit",
    );
    submit?.click();
    submit?.click();
  });
  const supersededCreateCount = () =>
    capturedMutations.filter((request) => {
      if (request.method !== "POST" || request.pathname !== "/api/pages/") {
        return false;
      }
      return (
        (JSON.parse(request.body ?? "{}") as LandingPagePayload).name ===
        supersededName
      );
    }).length;
  await expect.poll(supersededCreateCount).toBe(1);
  await expect(page.locator("#modal #modalSubmit")).toBeDisabled();
  await expect(page.locator("#name")).toBeDisabled();
  expect(
    await page
      .locator("#modal .gophish-code-editor")
      .evaluate((element) => (element as HTMLElement).inert),
  ).toBe(true);

  await closePageModal(page);
  await openPageModal(page, "New Page");
  await expect(page.locator("#modal #modalSubmit")).toBeDisabled();
  await page.evaluate(() => {
    (document.getElementById("name") as HTMLInputElement).value =
      "Current modal must survive";
  });
  releaseSupersededCreate();
  await expect(page.locator("#modal #modalSubmit")).toBeEnabled();
  await expect(page.locator("#name")).toHaveValue("Current modal must survive");
  await expect(page.locator('[id="modal.flashes"]').first()).not.toContainText(
    "superseded landing page create failure",
  );
  await closePageModal(page);

  const pageName = `LandingPageMutationBaseline_${Date.now()}`;
  const redirectURL = "https://redirect.invalid/after?source=landing";
  await openPageModal(page, "New Page");
  await page.locator("#name").fill(pageName);
  await setHTML(page, createdHTML);
  await page.locator("#capture_credentials_checkbox").check({ force: true });
  await page.locator("#capture_passwords_checkbox").uncheck({ force: true });
  await page.locator("#redirect_url_input").fill(redirectURL);

  await page.route(
    "**/api/pages/",
    (route) =>
      route.fulfill({
        body: JSON.stringify({ message: "synthetic landing page create failure" }),
        contentType: "application/json",
        status: 400,
      }),
    { times: 1 },
  );
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator('[id="modal.flashes"]').first()).toContainText(
    "synthetic landing page create failure",
  );
  await expect(page.locator("#name")).toHaveValue(pageName);
  await expect(page.locator("#capture_credentials_checkbox")).toBeChecked();
  await expect(page.locator("#capture_passwords_checkbox")).not.toBeChecked();
  await expect(page.locator("#redirect_url_input")).toHaveValue(redirectURL);
  await expect.poll(() => getHTML(page)).toBe(createdHTML);

  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeHidden();
  const createdRow = page
    .locator("#pagesTable tbody tr")
    .filter({ hasText: pageName });
  await expect(createdRow).toBeVisible();

  const createRequests = capturedMutations.filter(
    (request) =>
      request.method === "POST" &&
      request.pathname === "/api/pages/" &&
      (JSON.parse(request.body ?? "{}") as LandingPagePayload).name === pageName,
  );
  expect(createRequests).toHaveLength(2);
  expect(createRequests[1].body).toBe(createRequests[0].body);
  const createBody = JSON.parse(createRequests[0].body ?? "") as LandingPagePayload;
  expect(createBody).toEqual({
    capture_credentials: true,
    capture_passwords: false,
    html: createdHTML,
    name: pageName,
    redirect_url: redirectURL,
  });

  const editHandler =
    (await createdRow.locator('button[onclick^="edit("]').getAttribute("onclick")) ??
      "";
  const indexMatch = editHandler.match(/\d+/);
  if (!indexMatch) {
    throw new Error(`Unable to determine landing page index from ${editHandler}`);
  }
  const pageIndex = Number(indexMatch[0]);
  const pageId = await page.evaluate(
    (index) =>
      (
        window as unknown as {
          pages: Array<{ id: number }>;
        }
      ).pages[index].id,
    pageIndex,
  );

  await createdRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#modal")).toBeVisible();
  const updatedName = `${pageName}_updated`;
  const updatedRedirectURL = "";
  await page.locator("#name").fill(updatedName);
  await setHTML(page, updatedHTML);
  await page.locator("#capture_credentials_checkbox").check({ force: true });
  await page.locator("#capture_passwords_checkbox").check({ force: true });
  await page.locator("#redirect_url_input").fill(updatedRedirectURL);

  let releaseUpdateFailure: () => void = () => {};
  const updateFailureGate = new Promise<void>((resolve) => {
    releaseUpdateFailure = resolve;
  });
  await page.route(
    (url) => url.pathname === `/api/pages/${pageId}`,
    async (route) => {
      await updateFailureGate;
      await route.fulfill({
        body: JSON.stringify({ message: "synthetic landing page update failure" }),
        contentType: "application/json",
        status: 500,
      });
    },
    { times: 1 },
  );
  await page.evaluate(() => {
    (
      window as unknown as {
        pages: unknown[];
      }
    ).pages.reverse();
    const submit = document.querySelector<HTMLButtonElement>(
      "#modal #modalSubmit",
    );
    submit?.click();
    submit?.click();
  });
  const pendingUpdateCount = () =>
    capturedMutations.filter(
      (request) =>
        request.method === "PUT" &&
        request.pathname === `/api/pages/${pageId}` &&
        (JSON.parse(request.body ?? "{}") as LandingPagePayload).name ===
          updatedName,
    ).length;
  await expect.poll(pendingUpdateCount).toBe(1);
  await expect(page.locator("#modal #modalSubmit")).toBeDisabled();
  expect(
    await page
      .locator("#modal .gophish-code-editor")
      .evaluate((element) => (element as HTMLElement).inert),
  ).toBe(true);
  await page.evaluate(() => {
    (
      window as unknown as {
        pages: unknown[];
      }
    ).pages.reverse();
  });
  releaseUpdateFailure();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#modal #modalSubmit")).toBeEnabled();
  await expect(page.locator("#name")).toHaveValue(updatedName);
  await expect(page.locator("#capture_credentials_checkbox")).toBeChecked();
  await expect(page.locator("#capture_passwords_checkbox")).toBeChecked();
  await expect(page.locator("#redirect_url_input")).toHaveValue("");
  await expect.poll(() => getHTML(page)).toBe(updatedHTML);
  await expect(page.locator('[id="modal.flashes"]').first()).toHaveText("");

  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeHidden();
  const updatedRow = page
    .locator("#pagesTable tbody tr")
    .filter({ hasText: updatedName });
  await expect(updatedRow).toBeVisible();

  const updateRequests = capturedMutations.filter(
    (request) =>
      request.method === "PUT" &&
      request.pathname === `/api/pages/${pageId}` &&
      (JSON.parse(request.body ?? "{}") as LandingPagePayload).name ===
        updatedName,
  );
  expect(updateRequests).toHaveLength(2);
  expect(updateRequests[1].body).toBe(updateRequests[0].body);
  expect(JSON.parse(updateRequests[0].body ?? "")).toEqual({
    capture_credentials: true,
    capture_passwords: true,
    html: updatedHTML,
    id: pageId,
    name: updatedName,
    redirect_url: "",
  });

  await updatedRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#modal")).toBeVisible();
  await setHTML(page, canonicalSourceHTML);
  let releaseCanonicalUpdate: () => void = () => {};
  const canonicalUpdateGate = new Promise<void>((resolve) => {
    releaseCanonicalUpdate = resolve;
  });
  await page.route(
    (url) => url.pathname === `/api/pages/${pageId}`,
    async (route) => {
      await canonicalUpdateGate;
      await route.continue();
    },
    { times: 1 },
  );
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal #modalSubmit")).toBeDisabled();
  await closePageModal(page);
  await updatedRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#modal #modalSubmit")).toBeDisabled();
  releaseCanonicalUpdate();
  await expect(page.locator("#modal #modalSubmit")).toBeEnabled();
  await expect
    .poll(async () =>
      page.evaluate((id) => {
        const landingPages = (
          window as unknown as {
            pages: Array<{ html: string; id: number }>;
          }
        ).pages;
        return landingPages.find((landingPage) => landingPage.id === id)?.html;
      }, pageId),
    )
    .toContain("Canonical response");
  const storedCanonicalHTML = await page.evaluate((id) => {
    const landingPages = (
      window as unknown as {
        pages: Array<{ html: string; id: number }>;
      }
    ).pages;
    return landingPages.find((landingPage) => landingPage.id === id)?.html;
  }, pageId);
  expect(storedCanonicalHTML).not.toBe(canonicalSourceHTML);
  expect(storedCanonicalHTML).not.toContain("/must-be-rewritten");
  await expect.poll(() => getHTML(page)).toBe(storedCanonicalHTML);
  await closePageModal(page);

  await updatedRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#modal")).toBeVisible();
  const htmlBeforeClone = await getHTML(page);
  await page.locator('#modal button[onclick^="bsModalShow(\'#importSiteModal\'"]').click();
  await expect(page.locator("#importSiteModal")).toBeVisible();
  const cloneURL = "https://clone.invalid/source?campaign=synthetic";

  const supersededCloneURL = "https://clone.invalid/superseded";
  let releaseSupersededClone: () => void = () => {};
  const supersededCloneGate = new Promise<void>((resolve) => {
    releaseSupersededClone = resolve;
  });
  await page.route(
    "**/api/import/site",
    async (route) => {
      await supersededCloneGate;
      await route.fulfill({
        body: JSON.stringify({
          html: '<p id="superseded-clone">Superseded clone</p>',
        }),
        contentType: "application/json",
        status: 200,
      });
    },
    { times: 1 },
  );
  await page.locator("#url").fill(supersededCloneURL);
  await page.evaluate(() => {
    const submit = document.querySelector<HTMLButtonElement>(
      "#importSiteModal #modalSubmit",
    );
    submit?.click();
    submit?.click();
  });
  const supersededCloneCount = () =>
    capturedMutations.filter((request) => {
      if (request.pathname !== "/api/import/site") {
        return false;
      }
      return (
        (
          JSON.parse(request.body ?? "{}") as {
            url?: string;
          }
        ).url === supersededCloneURL
      );
    }).length;
  await expect.poll(supersededCloneCount).toBe(1);
  await expect(page.locator("#importSiteModal #modalSubmit")).toBeDisabled();
  await expect(page.locator("#url")).toBeDisabled();
  await page.locator("#importSiteModal .modal-footer .btn-secondary").click();
  await expect(page.locator("#importSiteModal")).toBeHidden();
  await page
    .locator('#modal button[onclick^="bsModalShow(\'#importSiteModal\'"]')
    .click();
  await expect(page.locator("#importSiteModal")).toBeVisible();
  await expect(page.locator("#importSiteModal #modalSubmit")).toBeDisabled();
  releaseSupersededClone();
  await expect(page.locator("#importSiteModal #modalSubmit")).toBeEnabled();
  await expect(page.locator("#url")).toBeEnabled();
  await expect.poll(() => getHTML(page)).toBe(htmlBeforeClone);

  await page.locator("#url").fill(cloneURL);

  await page.route(
    "**/api/import/site",
    (route) =>
      route.fulfill({
        body: JSON.stringify({ message: "synthetic site clone failure" }),
        contentType: "application/json",
        status: 500,
      }),
    { times: 1 },
  );
  await page.locator("#importSiteModal #modalSubmit").click();
  await expect(page.locator("#importSiteModal")).toBeVisible();
  await expect(
    page.locator('#importSiteModal [id="modal.flashes"]'),
  ).toContainText("synthetic site clone failure");
  await expect.poll(() => getHTML(page)).toBe(htmlBeforeClone);

  await page.route(
    "**/api/import/site",
    (route) =>
      route.fulfill({
        body: JSON.stringify({ html: clonedHTML }),
        contentType: "application/json",
        status: 200,
      }),
    { times: 1 },
  );
  await page.locator("#importSiteModal #modalSubmit").click();
  await expect(page.locator("#importSiteModal")).toBeHidden();
  await expect.poll(() => getHTML(page)).toBe(clonedHTML);
  await expect(page.locator("#synthetic-clone-marker")).toHaveCount(0);

  const preview = page.frameLocator("iframe[data-editor-preview]");
  await expect(preview.locator("#synthetic-clone-marker")).toBeVisible();
  await expect(preview.locator("script")).toHaveCount(0);
  await expect(preview.locator("#clone-image")).not.toHaveAttribute("src");
  await expect(preview.locator("#clone-image")).toHaveAttribute(
    "data-preview-src",
    "https://external.invalid/pixel.png",
  );
  await expect(preview.locator("#clone-form")).toHaveAttribute("inert", "");
  await expect(preview.locator("#clone-form")).not.toHaveAttribute("action");
  expect(
    await preview.locator("body").evaluate(
      () =>
        (
          window as unknown as {
            __cloneHandlerExecuted?: boolean;
            __cloneScriptExecuted?: boolean;
          }
        ).__cloneHandlerExecuted ||
        (
          window as unknown as {
            __cloneHandlerExecuted?: boolean;
            __cloneScriptExecuted?: boolean;
          }
        ).__cloneScriptExecuted,
    ),
  ).toBeUndefined();

  const cloneRequests = capturedMutations.filter(
    (request) =>
      request.pathname === "/api/import/site" &&
      (
        JSON.parse(request.body ?? "{}") as {
          url?: string;
        }
      ).url === cloneURL,
  );
  expect(cloneRequests).toHaveLength(2);
  expect(cloneRequests[1].body).toBe(cloneRequests[0].body);
  expect(JSON.parse(cloneRequests[0].body ?? "")).toEqual({
    include_resources: false,
    url: cloneURL,
  });
  await closePageModal(page);

  const deleteCount = () =>
    capturedMutations.filter(
      (request) =>
        request.method === "DELETE" &&
        request.pathname === `/api/pages/${pageId}`,
    ).length;
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
    (url) => url.pathname === `/api/pages/${pageId}`,
    async (route) => {
      await deleteFailureGate;
      await route.fulfill({
        body: JSON.stringify({ message: "synthetic landing page delete failure" }),
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
    "synthetic landing page delete failure",
  );
  await expect(updatedRow).toBeVisible();
  await page.evaluate(() => {
    (window as unknown as { Swal: { close: () => void } }).Swal.close();
  });
  await expect(page.locator(".swal2-popup")).toBeHidden();

  await updatedRow.locator("button.btn-danger").click();
  await page.locator(".swal2-confirm").click();
  await expect(page.locator(".swal2-popup")).toContainText(
    "Landing Page Deleted!",
  );
  expect(deleteCount()).toBe(2);
  await Promise.all([
    page.waitForNavigation({ waitUntil: "load" }),
    page.getByRole("button", { name: "OK" }).click(),
  ]);
  await expect(page.locator("#pagesTable")).not.toContainText(updatedName);

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

  const relevantPageErrors = pageErrors.filter(
    (message) =>
      !message.includes(
        "Service worker is disabled because the context is sandboxed",
      ),
  );
  expect(relevantPageErrors).toEqual([]);
  expect(externalRequests).toEqual([]);
});
