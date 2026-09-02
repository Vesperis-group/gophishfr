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

type DeferredRequest = {
  done: (callback: (data: unknown) => void) => DeferredRequest;
  fail: (
    callback: (error: {
      data?: unknown;
      responseJSON?: unknown;
      status?: number;
    }) => void,
  ) => DeferredRequest;
};

type RequestLike = DeferredRequest | Promise<unknown>;

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

test("webhook reads preserve their HTTP and settlement contracts", async ({ page }) => {
  const captured: CapturedRequest[] = [];
  let failureMode = false;

  await login(page);
  await page.route("**/api/webhooks/**", async (route) => {
    captured.push(captureRequest(route));
    await route.fulfill({
      body: JSON.stringify(
        failureMode
          ? { message: "synthetic webhook read failure" }
          : { name: new URL(route.request().url()).pathname },
      ),
      contentType: "application/json",
      status: failureMode ? 500 : 200,
    });
  });

  expect(await runInvocations(page)).toEqual([
    {
      data: { name: "/api/webhooks/" },
      name: "webhooks.get",
      settlement: "success",
    },
    {
      data: { name: "/api/webhooks/108" },
      name: "webhookId.get",
      settlement: "success",
    },
  ]);
  expect(captured).toEqual([
    {
      authorizationIsBearer: true,
      body: null,
      contentType: "application/json",
      method: "GET",
      pathname: "/api/webhooks/",
      search: "?{}",
    },
    {
      authorizationIsBearer: true,
      body: null,
      contentType: "application/json",
      method: "GET",
      pathname: "/api/webhooks/108",
      search: "?{}",
    },
  ]);

  captured.length = 0;
  failureMode = true;
  expect(await runInvocations(page)).toEqual([
    {
      data: { message: "synthetic webhook read failure" },
      name: "webhooks.get",
      settlement: "failure",
      status: 500,
    },
    {
      data: { message: "synthetic webhook read failure" },
      name: "webhookId.get",
      settlement: "failure",
      status: 500,
    },
  ]);
  expect(captured).toHaveLength(2);
});

test("webhook reads return native Promises without jQuery", async ({ page }) => {
  await login(page);
  await page.route("**/api/webhooks/**", (route) =>
    route.fulfill({
      body: JSON.stringify({ name: "native-webhook-read" }),
      contentType: "application/json",
      status: 200,
    }),
  );

  const results = await page.evaluate(async () => {
    type NativeRequest = Promise<unknown> & {
      always?: unknown;
      done?: unknown;
      fail?: unknown;
    };
    type API = {
      webhooks: { get: () => NativeRequest };
      webhookId: { get: (id: number) => NativeRequest };
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
        ["webhooks.get", globals.api.webhooks.get()],
        ["webhookId.get", globals.api.webhookId.get(108)],
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
  });

  expect(results).toEqual([
    {
      data: { name: "native-webhook-read" },
      hasAlways: false,
      hasDone: false,
      hasFail: false,
      name: "webhooks.get",
    },
    {
      data: { name: "native-webhook-read" },
      hasAlways: false,
      hasDone: false,
      hasFail: false,
      name: "webhookId.get",
    },
  ]);
});

async function runInvocations(page: Page): Promise<unknown[]> {
  return page.evaluate(async () => {
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
      webhooks: { get: () => Request };
      webhookId: { get: (id: number) => Request };
    };
    const api = (window as unknown as { api: API }).api;
    const requests: Array<[string, Request]> = [
      ["webhooks.get", api.webhooks.get()],
      ["webhookId.get", api.webhookId.get(108)],
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

    const results = [];
    for (const [name, request] of requests) {
      results.push(await settle(name, request));
    }
    return results;
  });
}
