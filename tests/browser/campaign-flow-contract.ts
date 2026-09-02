import { expect, type Page, type Route, test } from "@playwright/test";

type BrowserTelemetry = {
  consoleErrors: string[];
  failedLocalRequests: string[];
  failedLocalResponses: string[];
  pageErrors: string[];
};

type CampaignSummary = {
  id: number;
  name: string;
};

type RequestFailure = {
  data?: unknown;
  message: string;
  status: number;
};

type RecordedRequest = {
  body: string | null;
  method: string;
  path: string;
  query: string;
};

const transportCampaign = {
  groups: [{ name: "Browser Fixture Group" }, { name: "Browser Secondary Group" }],
  launch_date: "2031-04-15T09:30:00Z",
  name: "Browser campaign transport contract",
  page: { name: "Browser Fixture Landing Page" },
  send_by_date: "2031-04-15T10:30:00Z",
  smtp: { name: "Browser Fixture Sending Profile" },
  template: {
    attachments: [
      {
        content: "Zml4dHVyZQ==",
        name: "transport-fixture.txt",
        type: "text/plain",
      },
    ],
    name: "Browser Attachment Fixture",
  },
  url: "http://127.0.0.1:1",
};

const campaignSummaryURL = /\/api\/campaigns\/summary\?\{\}$/;

function isCampaignRoute(route: Route): boolean {
  return new URL(route.request().url()).pathname.startsWith("/api/campaigns/");
}

async function invokeCampaignWrappers(
  page: Page,
  mode: "delete" | "post" | "summary",
): Promise<unknown> {
  return page.evaluate(
    async ({ campaign, mode: selectedMode }) => {
      type Deferred<T> = {
        done: (callback: (value: T) => void) => Deferred<T>;
        fail: (
          callback: (error: {
            responseJSON?: unknown;
            status?: number;
            statusText?: string;
          }) => void,
        ) => Deferred<T>;
      };

      const settle = <T>(request: Promise<T> | Deferred<T>) => {
        if ("done" in request) {
          return new Promise<T>((resolve, reject) => {
            request.done(resolve).fail((error) => {
              reject({
                data: error.responseJSON,
                message: error.statusText || "Request failed",
                status: error.status || 0,
              });
            });
          });
        }
        return request;
      };

      let request: Promise<unknown> | Deferred<unknown>;
      if (selectedMode === "post") {
        request = api.campaigns.post(campaign);
      } else if (selectedMode === "summary") {
        request = api.campaigns.summary();
      } else {
        request = api.campaignId.delete(208);
      }

      try {
        return { ok: true, value: await settle(request) };
      } catch (error) {
        const failure = error as RequestFailure;
        return {
          ok: false,
          value: {
            data: failure.data,
            message: failure.message,
            status: failure.status,
          },
        };
      }
    },
    { campaign: transportCampaign, mode },
  );
}

async function assertTransportContracts(
  page: Page,
  telemetry: BrowserTelemetry,
  usesNativePromises: boolean,
): Promise<void> {
  await test.step("campaign wrappers preserve HTTP success contracts", async () => {
    const requests: RecordedRequest[] = [];
    await page.route("**/api/campaigns/**", async (route) => {
      if (!isCampaignRoute(route)) {
        await route.continue();
        return;
      }
      const request = route.request();
      const url = new URL(request.url());
      requests.push({
        body: request.postData(),
        method: request.method(),
        path: url.pathname,
        query: url.search,
      });
      if (request.method() === "POST") {
        await route.fulfill({
          contentType: "application/json",
          json: { ...transportCampaign, id: 206 },
          status: 201,
        });
      } else if (url.pathname.endsWith("/summary")) {
        await route.fulfill({
          contentType: "application/json",
          json: { campaigns: [{ id: 207, name: "summary fixture" }], total: 1 },
          status: 200,
        });
      } else {
        await route.fulfill({
          contentType: "application/json",
          json: { message: "campaign deleted" },
          status: 200,
        });
      }
    });

    const results = [];
    for (const mode of ["post", "summary", "delete"] as const) {
      results.push(await invokeCampaignWrappers(page, mode));
    }
    expect(results).toEqual([
      { ok: true, value: { ...transportCampaign, id: 206 } },
      {
        ok: true,
        value: { campaigns: [{ id: 207, name: "summary fixture" }], total: 1 },
      },
      { ok: true, value: { message: "campaign deleted" } },
    ]);
    expect(requests).toEqual([
      {
        body: JSON.stringify(transportCampaign),
        method: "POST",
        path: "/api/campaigns/",
        query: "",
      },
      {
        body: null,
        method: "GET",
        path: "/api/campaigns/summary",
        query: "?{}",
      },
      {
        body: "{}",
        method: "DELETE",
        path: "/api/campaigns/208",
        query: "",
      },
    ]);

    if (usesNativePromises) {
      expect(
        await page.evaluate(async (campaign) => {
          const originalAjax = window.jQuery.ajax;
          let ajaxCalls = 0;
          window.jQuery.ajax = () => {
            ajaxCalls += 1;
            throw new Error("campaign wrappers must not use jQuery.ajax");
          };
          try {
            await Promise.all([
              api.campaigns.post(campaign),
              api.campaigns.summary(),
              api.campaignId.delete(208),
            ]);
          } finally {
            window.jQuery.ajax = originalAjax;
          }
          return ajaxCalls;
        }, transportCampaign),
      ).toBe(0);
    }
    await page.unroute("**/api/campaigns/**");
  });

  await test.step("campaign wrappers preserve HTTP error contracts", async () => {
    const responseBaseline = telemetry.failedLocalResponses.length;
    const consoleBaseline = telemetry.consoleErrors.length;
    await page.route("**/api/campaigns/**", async (route) => {
      if (!isCampaignRoute(route)) {
        await route.continue();
        return;
      }
      await route.fulfill({
        contentType: "application/json",
        json: { message: "synthetic campaign HTTP failure" },
        status: 503,
      });
    });

    const results = [];
    for (const mode of ["post", "summary", "delete"] as const) {
      results.push(await invokeCampaignWrappers(page, mode));
    }
    await page.unroute("**/api/campaigns/**");

    expect(results).toEqual([
      {
        ok: false,
        value: {
          data: { message: "synthetic campaign HTTP failure" },
          message: "Service Unavailable",
          status: 503,
        },
      },
      {
        ok: false,
        value: {
          data: { message: "synthetic campaign HTTP failure" },
          message: "Service Unavailable",
          status: 503,
        },
      },
      {
        ok: false,
        value: {
          data: { message: "synthetic campaign HTTP failure" },
          message: "Service Unavailable",
          status: 503,
        },
      },
    ]);
    await expect
      .poll(() => telemetry.failedLocalResponses.length)
      .toBe(responseBaseline + 3);
    expect(telemetry.failedLocalResponses.slice(responseBaseline)).toEqual([
      "503 /api/campaigns/",
      "503 /api/campaigns/summary",
      "503 /api/campaigns/208",
    ]);
    telemetry.failedLocalResponses.length = responseBaseline;
    telemetry.consoleErrors.length = consoleBaseline;
  });

  await test.step("campaign wrappers preserve network error contracts", async () => {
    const requestBaseline = telemetry.failedLocalRequests.length;
    const consoleBaseline = telemetry.consoleErrors.length;
    await page.route("**/api/campaigns/**", async (route) => {
      if (!isCampaignRoute(route)) {
        await route.continue();
        return;
      }
      await route.abort("connectionrefused");
    });

    const results = [];
    for (const mode of ["post", "summary", "delete"] as const) {
      results.push(await invokeCampaignWrappers(page, mode));
    }
    await page.unroute("**/api/campaigns/**");

    for (const result of results) {
      expect(result).toEqual({
        ok: false,
        value: {
          data: undefined,
          message: expect.any(String),
          status: 0,
        },
      });
    }
    await expect.poll(() => telemetry.failedLocalRequests.length).toBe(requestBaseline + 3);
    expect(
      telemetry.failedLocalRequests
        .slice(requestBaseline)
        .map((entry) => entry.replace(/: .+$/, "")),
    ).toEqual([
      "POST /api/campaigns/",
      "GET /api/campaigns/summary",
      "DELETE /api/campaigns/208",
    ]);
    expect(telemetry.consoleErrors.slice(consoleBaseline)).toEqual([
      expect.stringContaining("ERR_CONNECTION_REFUSED"),
      expect.stringContaining("ERR_CONNECTION_REFUSED"),
      expect.stringContaining("ERR_CONNECTION_REFUSED"),
    ]);
    telemetry.failedLocalRequests.length = requestBaseline;
    telemetry.consoleErrors.length = consoleBaseline;
  });
}

async function assertLaunchConsumer(page: Page, usesNativePromises: boolean): Promise<void> {
  await test.step("campaign launch preserves its parent and settlement contracts", async () => {
    await page.getByRole("button", { name: "New Campaign" }).click();
    await expect(page.locator("#modal")).toBeVisible();
    await page.locator("#name").fill("Campaign consumer contract");
    await page.locator("#url").fill("http://127.0.0.1:1");
    await page.locator("#template").selectOption({ label: "Browser Fixture Template" });
    await page.locator("#page").selectOption({ label: "Browser Fixture Landing Page" });
    await page.locator("#profile").selectOption({
      label: "Browser Fixture Sending Profile",
    });
    await page.locator("#launch_date").fill("2031-04-15T09:30");
    await page.evaluate(() => {
      const select = document.querySelector("#users") as HTMLSelectElement & {
        tomselect?: {
          addItem: (value: string, silent?: boolean) => void;
          options: Record<string, { text?: string }>;
        };
      };
      const option = Object.entries(select.tomselect?.options ?? {}).find(
        ([, entry]) => entry.text === "Browser Fixture Group",
      );
      if (!option) {
        throw new Error("Browser Fixture Group option is missing");
      }
      select.tomselect?.addItem(option[0], true);
    });

    const contract = await page.evaluate(async (nativeMode) => {
      type SweetAlertConfiguration = {
        preConfirm: () => Promise<unknown>;
      };
      type JQueryRequest = {
        done: (callback: (value: { id: number }) => void) => JQueryRequest;
        fail: (callback: (error: unknown) => void) => JQueryRequest;
      };

      const originalFire = Swal.fire;
      const originalPost = api.campaigns.post;
      const originalCampaign = campaign;
      let configuration: SweetAlertConfiguration | undefined;
      let postCalls = 0;
      let resolveNative: ((value: { id: number }) => void) | undefined;
      const nativeRequest = new Promise<{ id: number }>((resolve) => {
        resolveNative = resolve;
      });
      const doneCallbacks: Array<(value: { id: number }) => void> = [];
      const jqRequest: JQueryRequest = {
        done(callback) {
          doneCallbacks.push(callback);
          return jqRequest;
        },
        fail() {
          return jqRequest;
        },
      };

      Swal.fire = ((options: SweetAlertConfiguration) => {
        configuration = options;
        return new Promise(() => undefined);
      }) as typeof Swal.fire;
      api.campaigns.post = (() => {
        postCalls += 1;
        return nativeMode ? nativeRequest : jqRequest;
      }) as typeof api.campaigns.post;

      const outerReturn = launch();
      if (!configuration) {
        throw new Error("launch did not configure SweetAlert");
      }
      const first = configuration.preConfirm();
      const second = configuration.preConfirm();
      const controlsDisabledDuringRequest = [
        "name",
        "template",
        "url",
        "page",
        "profile",
        "launch_date",
        "send_by_date",
        "users",
        "launchButton",
      ].every((id) => (document.getElementById(id) as HTMLInputElement).disabled);
      const response = { id: 209 };
      resolveNative?.(response);
      doneCallbacks.forEach((callback) => callback(response));
      await Promise.allSettled([first, second]);

      api.campaigns.post = (() => {
        if (nativeMode) {
          return Promise.reject({
            data: { message: "synthetic launch failure" },
            message: "Bad Request",
            status: 400,
          });
        }
        const failedRequest: JQueryRequest = {
          done() {
            return failedRequest;
          },
          fail(callback) {
            callback({
              responseJSON: { message: "synthetic launch failure" },
              status: 400,
              statusText: "Bad Request",
            });
            return failedRequest;
          },
        };
        return failedRequest;
      }) as typeof api.campaigns.post;
      configuration = undefined;
      launch();
      if (!configuration) {
        throw new Error("launch retry did not configure SweetAlert");
      }
      const failed = configuration.preConfirm();
      let failureState = "pending";
      failed.then(
        () => {
          failureState = "fulfilled";
        },
        () => {
          failureState = "rejected";
        },
      );
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();

      Swal.fire = originalFire;
      api.campaigns.post = originalPost;
      campaign = originalCampaign;
      return {
        controlsDisabledDuringRequest,
        failureState,
        outerReturnIsUndefined: outerReturn === undefined,
        postCalls,
      };
    }, usesNativePromises);

    expect(contract.outerReturnIsUndefined).toBe(true);
    expect(contract).toEqual(
      usesNativePromises
        ? {
            controlsDisabledDuringRequest: true,
            failureState: "fulfilled",
            outerReturnIsUndefined: true,
            postCalls: 1,
          }
        : {
            controlsDisabledDuringRequest: false,
            failureState: "pending",
            outerReturnIsUndefined: true,
            postCalls: 2,
          },
    );
    await expect(page.locator('[id="modal.flashes"]')).toContainText(
      "synthetic launch failure",
    );
    await page.evaluate(() => Swal.close());
    await page.locator('#modal [data-bs-dismiss="modal"]').first().click();
    await expect(page.locator("#modal")).toBeHidden();
  });
}

async function assertSweetAlertDeleteConsumer(
  page: Page,
  usesNativePromises: boolean,
  consumer: "list" | "results",
): Promise<void> {
  const contract = await page.evaluate(
    async ({ nativeMode, selectedConsumer }) => {
      type SweetAlertConfiguration = {
        preConfirm: () => Promise<unknown>;
      };
      type JQueryRequest = {
        done: (callback: (value: { message: string }) => void) => JQueryRequest;
        fail: (callback: (error: unknown) => void) => JQueryRequest;
      };

      const originalFire = Swal.fire;
      const originalDelete = api.campaignId.delete;
      const originalCampaigns = selectedConsumer === "list" ? campaigns.slice() : undefined;
      const originalCampaign = selectedConsumer === "results" ? campaign : undefined;
      const stableId = selectedConsumer === "list" ? campaigns[0].id : campaign.id;
      const replacementId = stableId + 10_000;
      let configuration: SweetAlertConfiguration | undefined;
      const requestedIds: number[] = [];
      let resolveNative: ((value: { message: string }) => void) | undefined;
      const nativeRequest = new Promise<{ message: string }>((resolve) => {
        resolveNative = resolve;
      });
      const doneCallbacks: Array<(value: { message: string }) => void> = [];
      const jqRequest: JQueryRequest = {
        done(callback) {
          doneCallbacks.push(callback);
          return jqRequest;
        },
        fail() {
          return jqRequest;
        },
      };

      Swal.fire = ((options: SweetAlertConfiguration) => {
        configuration = options;
        return new Promise(() => undefined);
      }) as typeof Swal.fire;
      api.campaignId.delete = ((id: number) => {
        requestedIds.push(id);
        return nativeMode ? nativeRequest : jqRequest;
      }) as typeof api.campaignId.delete;

      const outerReturn =
        selectedConsumer === "list" ? deleteCampaign(0) : deleteCampaign();
      if (!configuration) {
        throw new Error("delete consumer did not configure SweetAlert");
      }
      if (selectedConsumer === "list") {
        campaigns[0] = { ...campaigns[0], id: replacementId };
      } else {
        campaign = { ...campaign, id: replacementId };
      }
      const first = configuration.preConfirm();
      const second = configuration.preConfirm();
      const response = { message: "campaign deleted" };
      resolveNative?.(response);
      doneCallbacks.forEach((callback) => callback(response));
      await Promise.allSettled([first, second]);

      Swal.fire = originalFire;
      api.campaignId.delete = originalDelete;
      if (originalCampaigns) {
        campaigns = originalCampaigns;
      }
      if (originalCampaign) {
        campaign = originalCampaign;
      }
      return {
        outerReturnIsUndefined: outerReturn === undefined,
        requestedIds,
        stableId,
      };
    },
    { nativeMode: usesNativePromises, selectedConsumer: consumer },
  );

  expect(contract.outerReturnIsUndefined).toBe(true);
  expect(contract.requestedIds).toEqual(
    usesNativePromises
      ? [contract.stableId]
      : [contract.stableId + 10_000, contract.stableId + 10_000],
  );
}

async function assertDeleteConsumers(page: Page, usesNativePromises: boolean): Promise<void> {
  await test.step("campaign list delete captures identity and deduplicates settlement", async () => {
    await page.goto("/campaigns");
    await expect(page.locator("#loading")).toBeHidden();
    await assertSweetAlertDeleteConsumer(page, usesNativePromises, "list");
  });

  await test.step("campaign results delete captures identity and deduplicates settlement", async () => {
    const fixture = await page.evaluate(() => {
      const selected = (campaigns as CampaignSummary[]).find(
        (entry) => entry.name === "Browser Fixture Campaign",
      );
      if (!selected) {
        throw new Error("Browser Fixture Campaign is missing");
      }
      return selected;
    });
    await page.goto(`/campaigns/${fixture.id}`);
    await expect(page.getByRole("heading", { name: fixture.name })).toBeVisible();
    await assertSweetAlertDeleteConsumer(page, usesNativePromises, "results");
  });

  await test.step("dashboard delete remains silent on failure and deduplicates requests", async () => {
    await page.goto("/");
    await expect(page.locator("#loading")).toBeHidden();
    const contract = await page.evaluate(async (nativeMode) => {
      type DualRequest = {
        done: (callback: (value: { message: string }) => void) => DualRequest;
        then: (
          success?: (value: { message: string }) => void,
          failure?: (error: unknown) => void,
        ) => Promise<void>;
      };

      const originalConfirm = window.confirm;
      const originalDelete = api.campaignId.delete;
      const requestedIds: number[] = [];
      let usedFailureHandler = false;
      let failureHandler: ((error: unknown) => void) | undefined;
      const request: DualRequest = {
        done() {
          return request;
        },
        then(_success, failure) {
          usedFailureHandler = typeof failure === "function";
          failureHandler = failure;
          return Promise.resolve();
        },
      };

      window.confirm = () => true;
      api.campaignId.delete = ((id: number) => {
        requestedIds.push(id);
        return request;
      }) as typeof api.campaignId.delete;
      const stableId = campaigns[0].id;
      const firstReturn = deleteCampaign(0);
      const secondReturn = deleteCampaign(0);
      failureHandler?.({
        data: { message: "synthetic dashboard delete failure" },
        status: 503,
      });
      await Promise.resolve();
      await Promise.resolve();

      window.confirm = originalConfirm;
      api.campaignId.delete = originalDelete;
      return {
        firstReturnIsUndefined: firstReturn === undefined,
        requestedIds,
        stableId,
        usedFailureHandler,
      };
    }, usesNativePromises);

    expect(contract.firstReturnIsUndefined).toBe(true);
    expect(contract.requestedIds).toEqual(
      usesNativePromises ? [contract.stableId] : [contract.stableId, contract.stableId],
    );
    expect(contract.usedFailureHandler).toBe(usesNativePromises);
  });
}

async function assertSummaryConsumers(
  page: Page,
  telemetry: BrowserTelemetry,
  usesNativePromises: boolean,
): Promise<void> {
  await test.step("campaign summary failures preserve page-specific loading behavior", async () => {
    const responseBaseline = telemetry.failedLocalResponses.length;
    const consoleBaseline = telemetry.consoleErrors.length;
    await page.route(campaignSummaryURL, (route) =>
      route.fulfill({
        contentType: "application/json",
        json: { message: "synthetic campaign summary failure" },
        status: 503,
      }),
    );

    await page.goto("/campaigns");
    await expect(page.locator("#loading")).toBeHidden();
    await expect(page.locator("#flashes").first()).toContainText("Error fetching campaigns");

    await page.goto("/");
    await expect(page.locator("#loading")).toBeVisible();
    await expect(page.locator("#flashes")).toHaveCount(0);
    await page.unroute(campaignSummaryURL);

    await expect
      .poll(() => telemetry.failedLocalResponses.length)
      .toBe(responseBaseline + 2);
    expect(telemetry.failedLocalResponses.slice(responseBaseline)).toEqual([
      "503 /api/campaigns/summary",
      "503 /api/campaigns/summary",
    ]);
    telemetry.failedLocalResponses.length = responseBaseline;
    telemetry.consoleErrors.length = consoleBaseline;
  });

  if (!usesNativePromises) {
    return;
  }

  await test.step("native campaign summaries remain responsive during slow responses", async () => {
    for (const pathname of ["/campaigns", "/"]) {
      let releaseResponse: (() => void) | undefined;
      let markStarted: (() => void) | undefined;
      const responseGate = new Promise<void>((resolve) => {
        releaseResponse = resolve;
      });
      const requestStarted = new Promise<void>((resolve) => {
        markStarted = resolve;
      });
      let summaryRequests = 0;
      await page.route(campaignSummaryURL, async (route) => {
        summaryRequests += 1;
        markStarted?.();
        await responseGate;
        await route.fulfill({
          contentType: "application/json",
          json: { campaigns: [], total: 0 },
          status: 200,
        });
      });

      await page.goto(pathname, { waitUntil: "domcontentloaded" });
      await requestStarted;
      await expect(page.locator("#loading")).toBeVisible();
      expect(await page.evaluate(() => campaigns)).toEqual([]);
      releaseResponse?.();
      await expect(page.locator("#loading")).toBeHidden();
      await expect(page.locator("#emptyMessage").first()).toBeVisible();
      expect(summaryRequests).toBe(1);
      await page.unroute(campaignSummaryURL);
    }
  });
}

export async function assertCampaignFlowContract(
  page: Page,
  telemetry: BrowserTelemetry,
): Promise<void> {
  const usesNativePromises = await page.evaluate(
    () =>
      api.campaigns.post.toString().includes("requestJSON") &&
      api.campaigns.summary.toString().includes("requestJSON") &&
      api.campaignId.delete.toString().includes("requestJSON"),
  );
  expect(usesNativePromises).toBe(true);

  await assertTransportContracts(page, telemetry, usesNativePromises);
  await assertLaunchConsumer(page, usesNativePromises);
  await assertDeleteConsumers(page, usesNativePromises);
  await assertSummaryConsumers(page, telemetry, usesNativePromises);

  expect(telemetry.pageErrors).toEqual([]);
  await page.goto("/campaigns");
  await expect(page.locator("#loading")).toBeHidden();
}
