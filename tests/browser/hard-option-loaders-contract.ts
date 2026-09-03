import { expect, type Page, type Request, type Route, test } from "@playwright/test";

type Deferred = {
  promise: Promise<void>;
  resolve: () => void;
};

type LoaderName = "pages" | "smtp" | "templates";

type BrowserTelemetry = {
  consoleErrors: string[];
  failedLocalResponses: string[];
  pageErrors: string[];
};

type RecordedRequest = {
  accept: string | undefined;
  authorization: string | undefined;
  body: string | null;
  contentType: string | undefined;
  method: string;
  path: string;
  query: string;
  requestedWith: string | undefined;
};

const fixtures = {
  groups: {
    groups: [
      { id: 701, name: "Hard Loader Group", num_targets: 1 },
      { id: 702, name: "Hard Loader Secondary Group", num_targets: 2 },
    ],
  },
  pages: [
    {
      id: 711,
      modified_date: "2031-04-15T09:30:00Z",
      name: "Hard Loader Landing Page",
    },
  ],
  smtp: [
    {
      id: 721,
      interface_type: "SMTP",
      modified_date: "2031-04-15T09:30:00Z",
      name: "Hard Loader Sending Profile",
    },
  ],
  templates: [
    {
      id: 731,
      modified_date: "2031-04-15T09:30:00Z",
      name: "Hard Loader Template",
    },
  ],
};

const loaderDefinitions: Array<{
  fixture: unknown;
  name: LoaderName;
  path: string;
  route: string;
}> = [
  {
    fixture: fixtures.templates,
    name: "templates",
    path: "/api/templates/",
    route: "**/api/templates/**",
  },
  {
    fixture: fixtures.pages,
    name: "pages",
    path: "/api/pages/",
    route: "**/api/pages/**",
  },
  {
    fixture: fixtures.smtp,
    name: "smtp",
    path: "/api/smtp/",
    route: "**/api/smtp/**",
  },
];

function deferred(): Deferred {
  let release: (() => void) | undefined;
  const promise = new Promise<void>((resolve) => {
    release = resolve;
  });
  return {
    promise,
    resolve: () => {
      if (!release) {
        throw new Error("deferred response has no resolver");
      }
      release();
    },
  };
}

function loaderName(request: Request): LoaderName | undefined {
  const path = new URL(request.url()).pathname;
  return loaderDefinitions.find((definition) => definition.path === path)?.name;
}

function campaignTestEmailTrigger(page: Page) {
  return page.locator(
    `#modal button[onclick^="bsModalShow('#sendTestEmailModal'"]`,
  );
}

async function hideCampaignModal(page: Page): Promise<void> {
  const modal = page.locator("#modal");
  const hidden = modal.evaluate(
    (element) =>
      new Promise<void>((resolve) => {
        element.addEventListener("hidden.bs.modal", () => resolve(), { once: true });
      }),
  );
  await modal.locator('.modal-footer button[data-bs-dismiss="modal"]').click();
  await hidden;
  await expect(modal).toBeHidden();
}

async function waitForBrowserSettlements(page: Page): Promise<void> {
  await page.evaluate(
    () =>
      new Promise<void>((resolve) => {
        requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
      }),
  );
}

async function selectedOptionState(page: Page): Promise<{
  name: string;
  page: string;
  profile: string;
  template: string;
  url: string;
}> {
  return page.evaluate(() => {
    const selectedText = (id: string) => {
      const select = document.getElementById(id) as HTMLSelectElement;
      return select.options[select.selectedIndex]?.text ?? "";
    };
    return {
      name: (document.getElementById("name") as HTMLInputElement).value,
      page: selectedText("page"),
      profile: selectedText("profile"),
      template: selectedText("template"),
      url: (document.getElementById("url") as HTMLInputElement).value,
    };
  });
}

async function invokeLoaders(page: Page): Promise<
  Array<{
    nativePromise: boolean;
    value: unknown;
  }>
> {
  return page.evaluate(async () => {
    type LegacyRequest = {
      done: (callback: (value: unknown) => void) => LegacyRequest;
      fail: (callback: (error: unknown) => void) => LegacyRequest;
    };

    const settle = (request: Promise<unknown> | LegacyRequest) => {
      if ("done" in request) {
        return new Promise<unknown>((resolve, reject) => {
          request.done(resolve).fail(reject);
        });
      }
      return request;
    };
    const requests = [api.templates.get(), api.pages.get(), api.SMTP.get()];
    const values = [];
    for (const request of requests) {
      values.push({
        nativePromise: request instanceof Promise,
        value: await settle(request),
      });
    }
    return values;
  });
}

async function assertWrapperContracts(
  page: Page,
  expectedNativeTransport: boolean,
): Promise<void> {
  await test.step("S10, S15 and S20 preserve their exact GET contracts", async () => {
    await page.goto("/campaigns");
    await expect(page.locator("#loading")).toBeHidden();

    const requests: RecordedRequest[] = [];
    const capture = (request: Request) => {
      if (!loaderName(request)) {
        return;
      }
      const url = new URL(request.url());
      requests.push({
        accept: request.headers()["accept"],
        authorization: request.headers()["authorization"],
        body: request.postData(),
        contentType: request.headers()["content-type"],
        method: request.method(),
        path: url.pathname,
        query: url.search,
        requestedWith: request.headers()["x-requested-with"],
      });
    };
    page.on("request", capture);
    const results = await invokeLoaders(page);
    page.off("request", capture);

    expect(results.map(({ nativePromise }) => nativePromise)).toEqual([
      expectedNativeTransport,
      expectedNativeTransport,
      expectedNativeTransport,
    ]);
    expect(
      (results[0].value as Array<{ name: string }>).some(
        ({ name }) => name === "Browser Fixture Template",
      ),
    ).toBe(true);
    expect(
      (results[1].value as Array<{ name: string }>).some(
        ({ name }) => name === "Browser Fixture Landing Page",
      ),
    ).toBe(true);
    expect(
      (results[2].value as Array<{ name: string }>).some(
        ({ name }) => name === "Browser Fixture Sending Profile",
      ),
    ).toBe(true);
    expect(
      requests.map(({ body, method, path, query }) => ({ body, method, path, query })),
    ).toEqual([
      { body: null, method: "GET", path: "/api/templates/", query: "?{}" },
      { body: null, method: "GET", path: "/api/pages/", query: "?{}" },
      { body: null, method: "GET", path: "/api/smtp/", query: "?{}" },
    ]);
    for (const request of requests) {
      expect(request.accept).toBe("application/json, text/javascript, */*; q=0.01");
      expect(request.authorization).toBeUndefined();
      expect(request.contentType).toBe("application/json");
      expect(request.requestedWith === "XMLHttpRequest").toBe(!expectedNativeTransport);
    }
  });
}

async function installOptionRoutes(
  page: Page,
  handlers: Partial<Record<LoaderName, (route: Route) => Promise<void> | void>> = {},
): Promise<void> {
  await page.route("**/api/groups/summary*", (route) =>
    route.fulfill({ contentType: "application/json", json: fixtures.groups, status: 200 }),
  );
  for (const definition of loaderDefinitions) {
    await page.route(definition.route, (route) => {
      const handler = handlers[definition.name];
      if (handler) {
        return handler(route);
      }
      return route.fulfill({
        contentType: "application/json",
        json: definition.fixture,
        status: 200,
      });
    });
  }
}

async function removeOptionRoutes(page: Page): Promise<void> {
  await page.unroute("**/api/groups/summary*");
  for (const definition of loaderDefinitions) {
    await page.unroute(definition.route);
  }
}

async function assertControlledCampaignOrder(
  page: Page,
  expectedNativeTransport: boolean,
): Promise<void> {
  await test.step("new campaign preserves the controlled templates-pages-SMTP order", async () => {
    await page.goto("/campaigns");
    await expect(page.locator("#loading")).toBeHidden();

    const gates = {
      pages: deferred(),
      smtp: deferred(),
      templates: deferred(),
    };
    const started = {
      pages: deferred(),
      smtp: deferred(),
      templates: deferred(),
    };
    const order: string[] = [];
    await installOptionRoutes(page, {
      pages: async (route) => {
        order.push("pages");
        started.pages.resolve();
        await gates.pages.promise;
        await route.fulfill({
          contentType: "application/json",
          json: fixtures.pages,
          status: 200,
        });
      },
      smtp: async (route) => {
        order.push("smtp");
        started.smtp.resolve();
        await gates.smtp.promise;
        await route.fulfill({
          contentType: "application/json",
          json: fixtures.smtp,
          status: 200,
        });
      },
      templates: async (route) => {
        order.push("templates");
        started.templates.resolve();
        await gates.templates.promise;
        await route.fulfill({
          contentType: "application/json",
          json: fixtures.templates,
          status: 200,
        });
      },
    });

    const open = page.getByRole("button", { name: "New Campaign" }).click();
    await started.templates.promise;
    expect(order).toEqual(["templates"]);
    if (expectedNativeTransport) {
      await open;
      await expect(page.locator("#modal")).toBeVisible();
      await expect(page.locator("#launchButton")).toBeDisabled();
      await expect(campaignTestEmailTrigger(page)).toBeDisabled();
    }

    gates.templates.resolve();
    await started.pages.promise;
    expect(order).toEqual(["templates", "pages"]);
    gates.pages.resolve();
    await started.smtp.promise;
    expect(order).toEqual(["templates", "pages", "smtp"]);
    gates.smtp.resolve();
    await open;

    await expect(page.locator("#template")).toHaveValue(String(fixtures.templates[0].id));
    await expect(page.locator("#page")).toHaveValue(String(fixtures.pages[0].id));
    await expect(page.locator("#profile")).toHaveValue(String(fixtures.smtp[0].id));
    await expect(page.locator("#launchButton")).toBeEnabled();
    await expect(campaignTestEmailTrigger(page)).toBeEnabled();
    await hideCampaignModal(page);
    await removeOptionRoutes(page);
  });
}

async function assertCopyContract(page: Page): Promise<void> {
  await test.step("copy waits for every option before applying preselection", async () => {
    await page.goto("/campaigns");
    await expect(page.locator("#loading")).toBeHidden();
    const order: string[] = [];
    await installOptionRoutes(page);
    const capture = (request: Request) => {
      const name = loaderName(request);
      if (name) {
        order.push(name);
      } else if (new URL(request.url()).pathname === "/api/campaigns/801") {
        order.push("campaign");
      }
    };
    page.on("request", capture);
    await page.route("**/api/campaigns/801*", (route) =>
      route.fulfill({
        contentType: "application/json",
        json: {
          id: 801,
          name: "Hard Loader Source Campaign",
          page: { id: fixtures.pages[0].id, name: fixtures.pages[0].name },
          smtp: { id: fixtures.smtp[0].id, name: fixtures.smtp[0].name },
          template: {
            id: fixtures.templates[0].id,
            name: fixtures.templates[0].name,
          },
          url: "http://127.0.0.1:1/hard-loader",
        },
        status: 200,
      }),
    );
    const returnIsUndefined = await page.evaluate(() => {
      campaigns = [{ id: 801 }];
      return copy(0) === undefined;
    });
    expect(returnIsUndefined).toBe(true);
    await expect.poll(() => selectedOptionState(page)).toEqual({
      name: "Copy of Hard Loader Source Campaign",
      page: fixtures.pages[0].name,
      profile: fixtures.smtp[0].name,
      template: fixtures.templates[0].name,
      url: "http://127.0.0.1:1/hard-loader",
    });
    expect(order).toEqual(["templates", "pages", "smtp", "campaign"]);

    page.off("request", capture);
    await page.unroute("**/api/campaigns/801*");
    await removeOptionRoutes(page);
  });
}

async function assertFailureAndRetry(
  page: Page,
  expectedNativeTransport: boolean,
): Promise<void> {
  await test.step("each loader failure is handled and a copy retry succeeds", async () => {
    for (const failedLoader of loaderDefinitions) {
      await page.goto("/campaigns");
      await expect(page.locator("#loading")).toBeHidden();
      let campaignRequests = 0;
      await installOptionRoutes(page, {
        [failedLoader.name]: (route) =>
          route.fulfill({
            contentType: "application/json",
            json: { message: `synthetic ${failedLoader.name} option failure` },
            status: 503,
          }),
      });
      await page.route("**/api/campaigns/802*", (route) => {
        campaignRequests += 1;
        return route.fulfill({
          contentType: "application/json",
          json: {
            id: 802,
            name: "Failure Source",
            page: { id: fixtures.pages[0].id, name: fixtures.pages[0].name },
            smtp: { id: fixtures.smtp[0].id, name: fixtures.smtp[0].name },
            template: {
              id: fixtures.templates[0].id,
              name: fixtures.templates[0].name,
            },
            url: "http://127.0.0.1:1/failure",
          },
          status: 200,
        });
      });
      await page.evaluate(() => {
        campaigns = [{ id: 802 }];
        copy(0);
      });

      if (expectedNativeTransport) {
        await expect(page.locator('[id="modal.flashes"]')).toContainText(
          `synthetic ${failedLoader.name} option failure`,
        );
        expect(campaignRequests).toBe(0);
        await expect(page.locator("#launchButton")).toBeDisabled();
        await expect(campaignTestEmailTrigger(page)).toBeDisabled();
      } else {
        await expect.poll(() => campaignRequests).toBe(1);
      }

      await removeOptionRoutes(page);
      await installOptionRoutes(page);
      await page.evaluate(() => copy(0));
      await expect.poll(() => selectedOptionState(page)).toEqual({
        name: "Copy of Failure Source",
        page: fixtures.pages[0].name,
        profile: fixtures.smtp[0].name,
        template: fixtures.templates[0].name,
        url: "http://127.0.0.1:1/failure",
      });
      await expect(campaignTestEmailTrigger(page)).toBeEnabled();

      await page.unroute("**/api/campaigns/802*");
      await removeOptionRoutes(page);
    }
  });
}

async function assertListLoader(
  page: Page,
  definition: (typeof loaderDefinitions)[number],
  expectedNativeTransport: boolean,
): Promise<void> {
  const pageContract = {
    pages: {
      empty: "#emptyMessage",
      loading: "#loading",
      page: "/landing_pages",
      table: "#pagesTable",
      text: fixtures.pages[0].name,
      wrappers: "#pagesTable_wrapper",
    },
    smtp: {
      empty: "#emptyMessage",
      loading: "#loading",
      page: "/sending_profiles",
      table: "#profileTable",
      text: fixtures.smtp[0].name,
      wrappers: "#profileTable_wrapper",
    },
    templates: {
      empty: "#emptyMessage",
      loading: "#loading",
      page: "/templates",
      table: "#templateTable",
      text: fixtures.templates[0].name,
      wrappers: "#templateTable_wrapper",
    },
  }[definition.name];
  const gate = deferred();
  const started = deferred();
  let requests = 0;
  await page.route(definition.route, async (route) => {
    requests += 1;
    started.resolve();
    await gate.promise;
    await route.fulfill({
      contentType: "application/json",
      json: definition.fixture,
      status: 200,
    });
  });

  const navigation = page.goto(pageContract.page, { waitUntil: "domcontentloaded" });
  await started.promise;
  if (expectedNativeTransport) {
    await navigation;
    await expect(page.locator(pageContract.loading)).toBeVisible();
    await expect(page.locator(pageContract.table)).toBeHidden();
  }
  gate.resolve();
  await navigation;
  await expect(page.locator(pageContract.loading)).toBeHidden();
  await expect(page.locator(pageContract.table)).toBeVisible();
  await expect(page.locator(pageContract.table)).toContainText(pageContract.text);
  await expect(page.locator(pageContract.empty).first()).toBeHidden();
  expect(requests).toBe(1);
  expect(await page.locator(pageContract.wrappers).count()).toBe(1);

  await page.unroute(definition.route);

  const firstRequestFinished = deferred();
  const firstRequestGate = deferred();
  const firstRequestStarted = deferred();
  const staleName = `Stale ${definition.name} response`;
  const latestName = `Latest ${definition.name} response`;
  const renamedFixture = (name: string) =>
    (definition.fixture as Array<Record<string, unknown>>).map((record) => ({
      ...record,
      name,
    }));
  let overlappingRequests = 0;
  await page.route(definition.route, async (route) => {
    overlappingRequests += 1;
    const isFirstRequest = overlappingRequests === 1;
    if (isFirstRequest) {
      firstRequestStarted.resolve();
      await firstRequestGate.promise;
    }
    await route.fulfill({
      contentType: "application/json",
      json: renamedFixture(isFirstRequest ? staleName : latestName),
      status: 200,
    });
    if (isFirstRequest) {
      firstRequestFinished.resolve();
    }
  });

  await page.evaluate(() => load());
  await firstRequestStarted.promise;
  await page.evaluate(() => load());
  await expect(page.locator(pageContract.table)).toContainText(latestName);
  await expect(page.locator(pageContract.loading)).toBeHidden();
  firstRequestGate.resolve();
  await firstRequestFinished.promise;
  await waitForBrowserSettlements(page);
  expect(overlappingRequests).toBe(2);
  await expect(page.locator(pageContract.table)).toContainText(latestName);
  await expect(page.locator(pageContract.table)).not.toContainText(staleName);
  expect(await page.locator(pageContract.wrappers).count()).toBe(1);

  await page.unroute(definition.route);
}

async function assertListLoaders(
  page: Page,
  expectedNativeTransport: boolean,
): Promise<void> {
  await test.step("list loaders remain stable with controlled slow responses", async () => {
    for (const definition of loaderDefinitions) {
      await assertListLoader(page, definition, expectedNativeTransport);
    }
  });
}

async function assertNativeConcurrency(
  page: Page,
  expectedNativeTransport: boolean,
): Promise<void> {
  if (!expectedNativeTransport) {
    return;
  }

  await test.step("stale copy completion cannot overwrite the latest action", async () => {
    await page.goto("/campaigns");
    await expect(page.locator("#loading")).toBeHidden();
    let templateRequests = 0;
    const firstTemplateGate = deferred();
    const firstTemplateFinished = deferred();
    const firstTemplateStarted = deferred();
    await installOptionRoutes(page, {
      templates: async (route) => {
        templateRequests += 1;
        const isFirstRequest = templateRequests === 1;
        if (isFirstRequest) {
          firstTemplateStarted.resolve();
          await firstTemplateGate.promise;
        }
        await route.fulfill({
          contentType: "application/json",
          json: fixtures.templates,
          status: 200,
        });
        if (isFirstRequest) {
          firstTemplateFinished.resolve();
        }
      },
    });
    const detailRequests: number[] = [];
    await page.route(/\/api\/campaigns\/(803|804)\?\{\}$/, (route) => {
      const id = Number(new URL(route.request().url()).pathname.split("/").pop());
      detailRequests.push(id);
      return route.fulfill({
        contentType: "application/json",
        json: {
          id,
          name: id === 803 ? "Stale Copy" : "Latest Copy",
          page: { id: fixtures.pages[0].id, name: fixtures.pages[0].name },
          smtp: { id: fixtures.smtp[0].id, name: fixtures.smtp[0].name },
          template: {
            id: fixtures.templates[0].id,
            name: fixtures.templates[0].name,
          },
          url: `http://127.0.0.1:1/${id}`,
        },
        status: 200,
      });
    });

    await page.evaluate(() => {
      campaigns = [{ id: 803 }, { id: 804 }];
      copy(0);
    });
    await firstTemplateStarted.promise;
    await page.evaluate(() => copy(1));
    await expect.poll(() => selectedOptionState(page)).toEqual({
      name: "Copy of Latest Copy",
      page: fixtures.pages[0].name,
      profile: fixtures.smtp[0].name,
      template: fixtures.templates[0].name,
      url: "http://127.0.0.1:1/804",
    });
    firstTemplateGate.resolve();
    await expect.poll(() => templateRequests).toBe(2);
    await firstTemplateFinished.promise;
    await expect.poll(() => detailRequests).toEqual([804]);
    expect(await selectedOptionState(page)).toEqual({
      name: "Copy of Latest Copy",
      page: fixtures.pages[0].name,
      profile: fixtures.smtp[0].name,
      template: fixtures.templates[0].name,
      url: "http://127.0.0.1:1/804",
    });

    await page.unroute(/\/api\/campaigns\/(803|804)\?\{\}$/);
    await removeOptionRoutes(page);
  });

  await test.step("closing the modal invalidates a pending copy", async () => {
    await page.goto("/campaigns");
    await expect(page.locator("#loading")).toBeHidden();
    const firstTemplateFinished = deferred();
    const firstTemplateGate = deferred();
    const firstTemplateStarted = deferred();
    const hardRequests: LoaderName[] = [];
    const capture = (request: Request) => {
      const name = loaderName(request);
      if (name) {
        hardRequests.push(name);
      }
    };
    page.on("request", capture);
    await installOptionRoutes(page, {
      templates: async (route) => {
        firstTemplateStarted.resolve();
        await firstTemplateGate.promise;
        await route.fulfill({
          contentType: "application/json",
          json: fixtures.templates,
          status: 200,
        });
        firstTemplateFinished.resolve();
      },
    });
    let campaignRequests = 0;
    await page.route("**/api/campaigns/806*", (route) => {
      campaignRequests += 1;
      return route.fulfill({
        contentType: "application/json",
        json: { id: 806 },
        status: 200,
      });
    });

    await page.evaluate(() => {
      campaigns = [{ id: 806 }];
      bootstrap.Modal.getOrCreateInstance(document.getElementById("modal")).show();
      copy(0);
    });
    await firstTemplateStarted.promise;
    await expect(page.locator("#modal")).toBeVisible();
    await hideCampaignModal(page);
    firstTemplateGate.resolve();
    await firstTemplateFinished.promise;
    await waitForBrowserSettlements(page);
    expect(hardRequests).toEqual(["templates"]);
    expect(campaignRequests).toBe(0);
    expect(await selectedOptionState(page)).toEqual({
      name: "",
      page: "",
      profile: "",
      template: "",
      url: "",
    });

    page.off("request", capture);
    await page.unroute("**/api/campaigns/806*");
    await removeOptionRoutes(page);
  });

  await test.step("all three wrappers and copy work without jQuery", async () => {
    await page.goto("/campaigns");
    await expect(page.locator("#loading")).toBeHidden();
    await installOptionRoutes(page);
    await page.route("**/api/campaigns/805*", (route) =>
      route.fulfill({
        contentType: "application/json",
        json: {
          id: 805,
          name: "No jQuery Copy",
          page: { id: fixtures.pages[0].id, name: fixtures.pages[0].name },
          smtp: { id: fixtures.smtp[0].id, name: fixtures.smtp[0].name },
          template: {
            id: fixtures.templates[0].id,
            name: fixtures.templates[0].name,
          },
          url: "http://127.0.0.1:1/no-jquery",
        },
        status: 200,
      }),
    );
    await page.evaluate(() => {
      const scope = window as Window & {
        $?: unknown;
        jQuery?: unknown;
        __originalDollar?: unknown;
        __originalJQuery?: unknown;
      };
      scope.__originalDollar = scope.$;
      scope.__originalJQuery = scope.jQuery;
      scope.$ = undefined;
      scope.jQuery = undefined;
      campaigns = [{ id: 805 }];
      copy(0);
    });
    await expect.poll(() => selectedOptionState(page)).toEqual({
      name: "Copy of No jQuery Copy",
      page: fixtures.pages[0].name,
      profile: fixtures.smtp[0].name,
      template: fixtures.templates[0].name,
      url: "http://127.0.0.1:1/no-jquery",
    });
    const values = await invokeLoaders(page);
    expect(values.every(({ nativePromise }) => nativePromise)).toBe(true);
    await page.evaluate(() => {
      const scope = window as Window & {
        $?: unknown;
        jQuery?: unknown;
        __originalDollar?: unknown;
        __originalJQuery?: unknown;
      };
      scope.$ = scope.__originalDollar;
      scope.jQuery = scope.__originalJQuery;
      delete scope.__originalDollar;
      delete scope.__originalJQuery;
    });
    await page.unroute("**/api/campaigns/805*");
    await removeOptionRoutes(page);
  });
}

export async function assertHardOptionLoadersContract(
  page: Page,
  telemetry: BrowserTelemetry,
  expectedNativeTransport: boolean,
): Promise<void> {
  const consoleErrorBaseline = telemetry.consoleErrors.length;
  const failedResponseBaseline = telemetry.failedLocalResponses.length;
  const pageErrorBaseline = telemetry.pageErrors.length;
  await assertWrapperContracts(page, expectedNativeTransport);
  await assertControlledCampaignOrder(page, expectedNativeTransport);
  await assertCopyContract(page);
  await assertFailureAndRetry(page, expectedNativeTransport);
  await assertListLoaders(page, expectedNativeTransport);
  await assertNativeConcurrency(page, expectedNativeTransport);

  expect(telemetry.pageErrors.slice(pageErrorBaseline)).toEqual([]);
  expect(telemetry.failedLocalResponses.slice(failedResponseBaseline)).toEqual([
    "503 /api/templates/",
    "503 /api/pages/",
    "503 /api/smtp/",
  ]);
  expect(telemetry.consoleErrors.slice(consoleErrorBaseline)).toEqual([
    expect.stringContaining("503 (Service Unavailable)"),
    expect.stringContaining("503 (Service Unavailable)"),
    expect.stringContaining("503 (Service Unavailable)"),
  ]);
  telemetry.failedLocalResponses.length = failedResponseBaseline;
  telemetry.consoleErrors.length = consoleErrorBaseline;
  await page.goto("/campaigns");
  await expect(page.locator("#loading")).toBeHidden();
}
