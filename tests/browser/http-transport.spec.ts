import { expect, test, type Page, type Route } from "@playwright/test";

const username = requiredEnvironmentVariable("GOPHISHFR_BROWSER_USERNAME");
const password = requiredEnvironmentVariable("GOPHISHFR_BROWSER_PASSWORD");

type CapturedRequest = {
  accept: string | undefined;
  authorizationIsBearer: boolean;
  body: string | null;
  contentType: string | undefined;
  hasCookie: boolean;
  method: string;
  pathname: string;
  search: string;
  xRequestedWith: string | undefined;
};

type NativeRequestOutcome = {
  data?: unknown;
  dataType?: string;
  errorData?: unknown;
  errorName?: string;
  errorResponseText?: string;
  errorStatusText?: string;
  jqueryAbsent: boolean;
  message?: string;
  order: string[];
  settled: "failure" | "success";
  status?: number;
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
    accept: headers.accept,
    authorizationIsBearer: /^Bearer \S+$/.test(headers.authorization ?? ""),
    body: request.postData(),
    contentType: headers["content-type"],
    hasCookie: Boolean(headers.cookie),
    method: request.method(),
    pathname: url.pathname,
    search: url.search,
    xRequestedWith: headers["x-requested-with"],
  };
}

async function fulfillContractRequest(
  route: Route,
  captured: CapturedRequest[],
): Promise<void> {
  const request = captureRequest(route);
  captured.push(request);
  switch (request.pathname) {
    case "/api/browser-query-contract/success-get":
      await route.fulfill({
        body: JSON.stringify({ method: "GET", ok: true }),
        contentType: "application/json",
        status: 200,
      });
      return;
    case "/api/browser-query-contract/success-post":
      await route.fulfill({
        body: JSON.stringify({ method: "POST", ok: true }),
        contentType: "application/json",
        status: 200,
      });
      return;
    case "/api/browser-query-contract/no-content":
      await route.fulfill({ status: 204 });
      return;
    case "/api/browser-query-contract/empty-body":
      await route.fulfill({
        body: "",
        contentType: "application/json",
        status: 200,
      });
      return;
    case "/api/browser-query-contract/bad-request":
      await route.fulfill({
        body: JSON.stringify({ message: "synthetic bad request" }),
        contentType: "application/json",
        status: 400,
      });
      return;
    case "/api/browser-query-contract/not-found":
      await route.fulfill({
        body: JSON.stringify({ message: "synthetic not found" }),
        contentType: "application/json",
        status: 404,
      });
      return;
    case "/api/browser-query-contract/server-error":
      await route.fulfill({
        body: JSON.stringify({ message: "synthetic server error" }),
        contentType: "application/json",
        status: 500,
      });
      return;
    case "/api/browser-query-contract/invalid-json":
      await route.fulfill({
        body: "{invalid json",
        contentType: "application/json",
        status: 200,
      });
      return;
    case "/api/browser-query-contract/network-error":
      await route.abort("connectionfailed");
      return;
    default:
      throw new Error(`unexpected query-contract request: ${request.pathname}`);
  }
}

async function runNativeRequest(
  page: Page,
  endpoint: string,
  method: string,
  data?: unknown,
): Promise<NativeRequestOutcome> {
  return page.evaluate(
    async ({ requestData, requestEndpoint, requestMethod }) => {
      const globals = window as unknown as {
        $?: unknown;
        jQuery?: unknown;
        requestJSON: (
          endpoint: string,
          method: string,
          data: unknown,
        ) => Promise<unknown>;
      };
      const originalDollar = globals.$;
      const originalJQuery = globals.jQuery;
      const order = ["before"];
      globals.$ = undefined;
      globals.jQuery = undefined;
      try {
        const request = globals.requestJSON(
          requestEndpoint,
          requestMethod,
          requestData,
        );
        order.push("after-call");
        try {
          const responseData = await request;
          order.push("then");
          return {
            data: responseData,
            dataType: typeof responseData,
            jqueryAbsent:
              typeof globals.$ === "undefined" &&
              typeof globals.jQuery === "undefined",
            order,
            settled: "success" as const,
          };
        } catch (error) {
          order.push("catch");
          const requestError = error as {
            data?: unknown;
            message?: string;
            name?: string;
            responseText?: string;
            status?: number;
            statusText?: string;
          };
          return {
            errorData: requestError.data,
            errorName: requestError.name,
            errorResponseText: requestError.responseText,
            errorStatusText: requestError.statusText,
            jqueryAbsent:
              typeof globals.$ === "undefined" &&
              typeof globals.jQuery === "undefined",
            message: requestError.message,
            order,
            settled: "failure" as const,
            status: requestError.status,
          };
        }
      } finally {
        globals.$ = originalDollar;
        globals.jQuery = originalJQuery;
      }
    },
    { requestData: data, requestEndpoint: endpoint, requestMethod: method },
  );
}

test("native JSON transport preserves the HTTP contract without jQuery", async ({
  page,
}) => {
  const captured: CapturedRequest[] = [];
  const unexpectedConsoleErrors: string[] = [];
  page.on("console", (message) => {
    if (
      message.type() === "error" &&
      !["status of 400", "status of 404", "status of 500", "ERR_CONNECTION_FAILED"].some(
        (expected) => message.text().includes(expected),
      )
    ) {
      unexpectedConsoleErrors.push(message.text());
    }
  });
  await login(page);
  await page.route("**/api/browser-query-contract/**", (route) =>
    fulfillContractRequest(route, captured),
  );

  const getResult = await runNativeRequest(
    page,
    "/browser-query-contract/success-get",
    "GET",
    {},
  );
  expect(getResult).toEqual({
    data: { method: "GET", ok: true },
    dataType: "object",
    jqueryAbsent: true,
    order: ["before", "after-call", "then"],
    settled: "success",
  });

  const postResult = await runNativeRequest(
    page,
    "/browser-query-contract/success-post",
    "POST",
    { enabled: false, items: [0, null, ""] },
  );
  expect(postResult).toMatchObject({
    data: { method: "POST", ok: true },
    jqueryAbsent: true,
    order: ["before", "after-call", "then"],
    settled: "success",
  });
  await runNativeRequest(
    page,
    "/browser-query-contract/success-post",
    "POST",
    null,
  );
  await runNativeRequest(
    page,
    "/browser-query-contract/success-post",
    "POST",
  );

  const noContent = await runNativeRequest(
    page,
    "/browser-query-contract/no-content",
    "DELETE",
  );
  expect(noContent).toEqual({
    dataType: "undefined",
    jqueryAbsent: true,
    order: ["before", "after-call", "then"],
    settled: "success",
  });

  const emptyBody = await runNativeRequest(
    page,
    "/browser-query-contract/empty-body",
    "GET",
    {},
  );
  expect(emptyBody).toEqual({
    errorName: "APIRequestError",
    errorResponseText: "",
    errorStatusText: "parsererror",
    jqueryAbsent: true,
    message: "Invalid JSON response",
    order: ["before", "after-call", "catch"],
    settled: "failure",
    status: 200,
  });

  for (const [path, status, message] of [
    ["bad-request", 400, "synthetic bad request"],
    ["not-found", 404, "synthetic not found"],
    ["server-error", 500, "synthetic server error"],
  ] as const) {
    const result = await runNativeRequest(
      page,
      `/browser-query-contract/${path}`,
      "GET",
      {},
    );
    expect(result).toMatchObject({
      errorData: { message },
      errorName: "APIRequestError",
      jqueryAbsent: true,
      order: ["before", "after-call", "catch"],
      settled: "failure",
      status,
    });
  }

  const invalidJSON = await runNativeRequest(
    page,
    "/browser-query-contract/invalid-json",
    "GET",
    {},
  );
  expect(invalidJSON).toEqual({
    errorName: "APIRequestError",
    errorResponseText: "{invalid json",
    errorStatusText: "parsererror",
    jqueryAbsent: true,
    message: "Invalid JSON response",
    order: ["before", "after-call", "catch"],
    settled: "failure",
    status: 200,
  });

  const networkError = await runNativeRequest(
    page,
    "/browser-query-contract/network-error",
    "GET",
    {},
  );
  expect(networkError).toEqual({
    errorName: "APIRequestError",
    errorResponseText: "",
    errorStatusText: "error",
    jqueryAbsent: true,
    message: "Network request failed",
    order: ["before", "after-call", "catch"],
    settled: "failure",
    status: 0,
  });

  expect(captured.slice(0, 5)).toEqual([
    {
      accept: "application/json, text/javascript, */*; q=0.01",
      authorizationIsBearer: true,
      body: null,
      contentType: "application/json",
      hasCookie: true,
      method: "GET",
      pathname: "/api/browser-query-contract/success-get",
      search: "?{}",
      xRequestedWith: undefined,
    },
    {
      accept: "application/json, text/javascript, */*; q=0.01",
      authorizationIsBearer: true,
      body: '{"enabled":false,"items":[0,null,""]}',
      contentType: "application/json",
      hasCookie: true,
      method: "POST",
      pathname: "/api/browser-query-contract/success-post",
      search: "",
      xRequestedWith: undefined,
    },
    {
      accept: "application/json, text/javascript, */*; q=0.01",
      authorizationIsBearer: true,
      body: "null",
      contentType: "application/json",
      hasCookie: true,
      method: "POST",
      pathname: "/api/browser-query-contract/success-post",
      search: "",
      xRequestedWith: undefined,
    },
    {
      accept: "application/json, text/javascript, */*; q=0.01",
      authorizationIsBearer: true,
      body: null,
      contentType: "application/json",
      hasCookie: true,
      method: "POST",
      pathname: "/api/browser-query-contract/success-post",
      search: "",
      xRequestedWith: undefined,
    },
    {
      accept: "application/json, text/javascript, */*; q=0.01",
      authorizationIsBearer: true,
      body: null,
      contentType: "application/json",
      hasCookie: true,
      method: "DELETE",
      pathname: "/api/browser-query-contract/no-content",
      search: "",
      xRequestedWith: undefined,
    },
  ]);
  expect(captured).toHaveLength(11);
  expect(unexpectedConsoleErrors).toEqual([]);
});
