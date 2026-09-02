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

type WrapperInvocation = {
  body: unknown;
  method: string;
  name: string;
  pathname: string;
};

const invocations: WrapperInvocation[] = [
  {
    body: null,
    method: "GET",
    name: "campaignId.get",
    pathname: "/api/campaigns/101",
  },
  {
    body: null,
    method: "GET",
    name: "campaignId.results",
    pathname: "/api/campaigns/102/results",
  },
  {
    body: null,
    method: "GET",
    name: "campaignId.complete",
    pathname: "/api/campaigns/103/complete",
  },
  {
    body: null,
    method: "GET",
    name: "campaignId.summary",
    pathname: "/api/campaigns/104/summary",
  },
  {
    body: null,
    method: "GET",
    name: "groups.summary",
    pathname: "/api/groups/summary",
  },
  {
    body: {
      host: "imap.localhost.invalid",
      ignore_cert_errors: false,
      password: "synthetic-password",
      port: "993",
      tls: true,
      username: "fixture",
    },
    method: "POST",
    name: "IMAP.validate",
    pathname: "/api/imap/validate",
  },
  {
    body: {
      account_locked: false,
      password: "SyntheticFixture#1",
      password_change_required: true,
      role: "user",
      username: "fixture-create",
    },
    method: "POST",
    name: "users.post",
    pathname: "/api/users/",
  },
  {
    body: {
      account_locked: true,
      id: 105,
      password: "",
      password_change_required: false,
      role: "admin",
      username: "fixture-update",
    },
    method: "PUT",
    name: "userId.put",
    pathname: "/api/users/105",
  },
  {
    body: {},
    method: "DELETE",
    name: "userId.delete",
    pathname: "/api/users/106",
  },
  {
    body: {
      id: 107,
      is_active: true,
      name: "fixture-webhook",
      secret: "synthetic-secret",
      url: "http://127.0.0.1:1/hook",
    },
    method: "PUT",
    name: "webhookId.put",
    pathname: "/api/webhooks/107",
  },
  {
    body: {},
    method: "POST",
    name: "reset",
    pathname: "/api/reset",
  },
];

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

test("remaining async API wrappers preserve their request and settlement contracts", async ({
  page,
}) => {
  const captured: CapturedRequest[] = [];
  let failureMode = false;

  await login(page);
  await page.route("**/api/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    const invocation = invocations.find((candidate) => candidate.pathname === pathname);
    if (!invocation) {
      await route.continue();
      return;
    }

    captured.push(captureRequest(route));
    await route.fulfill({
      body: JSON.stringify(
        failureMode
          ? { message: `synthetic failure: ${invocation.name}` }
          : { name: invocation.name },
      ),
      contentType: "application/json",
      status: failureMode ? 400 : 200,
    });
  });

  const successResults = await runInvocations(page);
  expect(successResults).toEqual(
    invocations.map(({ name }) => ({
      data: { name },
      name,
      settlement: "success",
    })),
  );

  const expectedRequests = invocations.map(({ body, method, pathname }) => ({
    authorizationIsBearer: true,
    body: body === null ? null : JSON.stringify(body),
    contentType: "application/json",
    method,
    pathname,
    search: method === "GET" ? "?{}" : "",
  }));
  expect(captured).toHaveLength(expectedRequests.length);
  expect(captured).toEqual(expect.arrayContaining(expectedRequests));

  captured.length = 0;
  failureMode = true;
  const failureResults = await runInvocations(page);
  expect(failureResults).toEqual(
    invocations.map(({ name }) => ({
      data: { message: `synthetic failure: ${name}` },
      name,
      settlement: "failure",
      status: 400,
    })),
  );
  expect(captured).toHaveLength(invocations.length);
});

test("migrated API wrappers execute without jQuery", async ({ page }) => {
  await login(page);
  await page.route("**/api/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    const invocation = invocations.find((candidate) => candidate.pathname === pathname);
    if (!invocation) {
      await route.continue();
      return;
    }
    await route.fulfill({
      body: JSON.stringify({ name: invocation.name }),
      contentType: "application/json",
      status: 200,
    });
  });

  const results = await page.evaluate(async () => {
    type NativeRequest = Promise<{ name: string }> & {
      always?: unknown;
      done?: unknown;
      fail?: unknown;
    };
    type NativeAPI = {
      IMAP: { validate: (data: unknown) => NativeRequest };
      campaignId: {
        complete: (id: number) => NativeRequest;
        get: (id: number) => NativeRequest;
        results: (id: number) => NativeRequest;
      };
      groups: { summary: () => NativeRequest };
      reset: () => NativeRequest;
      userId: {
        delete: (id: number) => NativeRequest;
        put: (data: unknown) => NativeRequest;
      };
      users: { post: (data: unknown) => NativeRequest };
      webhookId: { put: (data: unknown) => NativeRequest };
    };

    const globals = window as unknown as {
      $?: unknown;
      api: NativeAPI;
      jQuery?: unknown;
    };
    const originalDollar = globals.$;
    const originalJQuery = globals.jQuery;

    try {
      globals.$ = undefined;
      globals.jQuery = undefined;
      const requests: Array<[string, NativeRequest]> = [
        ["campaignId.get", globals.api.campaignId.get(101)],
        ["campaignId.results", globals.api.campaignId.results(102)],
        ["campaignId.complete", globals.api.campaignId.complete(103)],
        ["groups.summary", globals.api.groups.summary()],
        [
          "IMAP.validate",
          globals.api.IMAP.validate({
            host: "imap.localhost.invalid",
            ignore_cert_errors: false,
            password: "synthetic-password",
            port: "993",
            tls: true,
            username: "fixture",
          }),
        ],
        [
          "users.post",
          globals.api.users.post({
            account_locked: false,
            password: "SyntheticFixture#1",
            password_change_required: true,
            role: "user",
            username: "fixture-create",
          }),
        ],
        [
          "userId.put",
          globals.api.userId.put({
            account_locked: true,
            id: 105,
            password: "",
            password_change_required: false,
            role: "admin",
            username: "fixture-update",
          }),
        ],
        ["userId.delete", globals.api.userId.delete(106)],
        [
          "webhookId.put",
          globals.api.webhookId.put({
            id: 107,
            is_active: true,
            name: "fixture-webhook",
            secret: "synthetic-secret",
            url: "http://127.0.0.1:1/hook",
          }),
        ],
        ["reset", globals.api.reset()],
      ];

      const settled = [];
      for (const [name, request] of requests) {
        const data = await request;
        settled.push({
          data,
          hasAlways: typeof request.always === "function",
          hasDone: typeof request.done === "function",
          hasFail: typeof request.fail === "function",
          name,
        });
      }
      return settled;
    } finally {
      globals.$ = originalDollar;
      globals.jQuery = originalJQuery;
    }
  });

  expect(results).toEqual(
    invocations
      .filter(({ name }) => name !== "campaignId.summary")
      .map(({ name }) => ({
        data: { name },
        hasAlways: false,
        hasDone: false,
        hasFail: false,
        name,
      })),
  );
});

async function runInvocations(page: Page): Promise<unknown[]> {
  return page.evaluate(async () => {
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
    type API = {
      IMAP: { validate: (data: unknown) => RequestLike };
      campaignId: {
        complete: (id: number) => RequestLike;
        get: (id: number) => RequestLike;
        results: (id: number) => RequestLike;
        summary: (id: number) => RequestLike;
      };
      groups: { summary: () => RequestLike };
      reset: () => RequestLike;
      userId: {
        delete: (id: number) => RequestLike;
        put: (data: unknown) => RequestLike;
      };
      users: { post: (data: unknown) => RequestLike };
      webhookId: { put: (data: unknown) => RequestLike };
    };

    const api = (window as unknown as { api: API }).api;
    const requests: Array<[string, RequestLike]> = [
      ["campaignId.get", api.campaignId.get(101)],
      ["campaignId.results", api.campaignId.results(102)],
      ["campaignId.complete", api.campaignId.complete(103)],
      ["campaignId.summary", api.campaignId.summary(104)],
      ["groups.summary", api.groups.summary()],
      [
        "IMAP.validate",
        api.IMAP.validate({
          host: "imap.localhost.invalid",
          ignore_cert_errors: false,
          password: "synthetic-password",
          port: "993",
          tls: true,
          username: "fixture",
        }),
      ],
      [
        "users.post",
        api.users.post({
          account_locked: false,
          password: "SyntheticFixture#1",
          password_change_required: true,
          role: "user",
          username: "fixture-create",
        }),
      ],
      [
        "userId.put",
        api.userId.put({
          account_locked: true,
          id: 105,
          password: "",
          password_change_required: false,
          role: "admin",
          username: "fixture-update",
        }),
      ],
      ["userId.delete", api.userId.delete(106)],
      [
        "webhookId.put",
        api.webhookId.put({
          id: 107,
          is_active: true,
          name: "fixture-webhook",
          secret: "synthetic-secret",
          url: "http://127.0.0.1:1/hook",
        }),
      ],
      ["reset", api.reset()],
    ];

    const settle = (name: string, request: RequestLike) => {
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
