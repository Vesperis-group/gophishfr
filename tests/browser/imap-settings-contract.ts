import { expect, type Page, type Route, test } from "@playwright/test";

type BrowserTelemetry = {
  consoleErrors: string[];
  failedLocalRequests: string[];
  failedLocalResponses: string[];
  pageErrors: string[];
};

type RecordedRequest = {
  authenticated: boolean;
  body: string | null;
  contentType: string | undefined;
  method: string;
  path: string;
  query: string;
};

type RequestFailure = {
  data?: unknown;
  message: string;
  status: number;
};

const syntheticResponseSettings = {
  delete_reported_campaign_email: true,
  enabled: true,
  folder: "Synthetic Reports",
  host: "imap.example.invalid",
  ignore_cert_errors: false,
  imap_freq: "90",
  port: "993",
  restrict_domain: "example.invalid",
  tls: true,
  username: "synthetic-user",
};

const syntheticSettings = {
  ...syntheticResponseSettings,
  password: "synthetic-password-123",
};

function isIMAPSettingsRoute(route: Route): boolean {
  return new URL(route.request().url()).pathname === "/api/imap/";
}

async function invokeIMAPWrapper(page: Page, mode: "get" | "post"): Promise<unknown> {
  return page.evaluate(
    async ({ mode: selectedMode, settings }) => {
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

      const request =
        selectedMode === "get" ? api.IMAP.get() : api.IMAP.post(settings);
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
    { mode, settings: syntheticSettings },
  );
}

async function assertTransportContracts(
  page: Page,
  telemetry: BrowserTelemetry,
  usesNativePromises: boolean,
): Promise<void> {
  await test.step("IMAP wrappers preserve HTTP success contracts", async () => {
    const requests: RecordedRequest[] = [];
    await page.route("**/api/imap/**", async (route) => {
      if (!isIMAPSettingsRoute(route)) {
        await route.continue();
        return;
      }
      const request = route.request();
      const url = new URL(request.url());
      requests.push({
        authenticated: /^Bearer \S+$/.test(request.headers()["authorization"] ?? ""),
        body: request.postData(),
        contentType: request.headers()["content-type"],
        method: request.method(),
        path: url.pathname,
        query: url.search,
      });
      await route.fulfill({
        contentType: "application/json",
        json:
          request.method() === "GET"
            ? [{ ...syntheticResponseSettings, last_login: "2026-09-02T10:00:00Z" }]
            : { message: "Successfully saved IMAP settings.", success: true },
        status: request.method() === "GET" ? 200 : 201,
      });
    });

    const getResult = await invokeIMAPWrapper(page, "get");
    const postResult = await invokeIMAPWrapper(page, "post");
    await page.unroute("**/api/imap/**");

    expect(getResult).toEqual({
      ok: true,
      value: [{ ...syntheticResponseSettings, last_login: "2026-09-02T10:00:00Z" }],
    });
    expect(postResult).toEqual({
      ok: true,
      value: { message: "Successfully saved IMAP settings.", success: true },
    });
    expect(requests).toEqual([
      {
        authenticated: true,
        body: null,
        contentType: "application/json",
        method: "GET",
        path: "/api/imap/",
        query: "?{}",
      },
      {
        authenticated: true,
        body: JSON.stringify(syntheticSettings),
        contentType: "application/json",
        method: "POST",
        path: "/api/imap/",
        query: "",
      },
    ]);
  });

  await test.step("IMAP wrappers preserve HTTP and network failures", async () => {
    const responseBaseline = telemetry.failedLocalResponses.length;
    const consoleBaseline = telemetry.consoleErrors.length;
    await page.route("**/api/imap/**", async (route) => {
      if (!isIMAPSettingsRoute(route)) {
        await route.continue();
        return;
      }
      await route.fulfill({
        contentType: "application/json",
        json: { message: "synthetic IMAP request failure" },
        status: 503,
      });
    });

    expect(await invokeIMAPWrapper(page, "get")).toEqual({
      ok: false,
      value: {
        data: { message: "synthetic IMAP request failure" },
        message: "Service Unavailable",
        status: 503,
      },
    });
    expect(await invokeIMAPWrapper(page, "post")).toEqual({
      ok: false,
      value: {
        data: { message: "synthetic IMAP request failure" },
        message: "Service Unavailable",
        status: 503,
      },
    });
    await page.unroute("**/api/imap/**");

    await expect
      .poll(() => telemetry.failedLocalResponses.length)
      .toBe(responseBaseline + 2);
    expect(telemetry.failedLocalResponses.slice(responseBaseline)).toEqual([
      "503 /api/imap/",
      "503 /api/imap/",
    ]);
    telemetry.failedLocalResponses.length = responseBaseline;
    telemetry.consoleErrors.length = consoleBaseline;

    const requestBaseline = telemetry.failedLocalRequests.length;
    await page.route("**/api/imap/**", async (route) => {
      if (!isIMAPSettingsRoute(route)) {
        await route.continue();
        return;
      }
      await route.abort("connectionrefused");
    });
    for (const mode of ["get", "post"] as const) {
      const result = (await invokeIMAPWrapper(page, mode)) as {
        ok: boolean;
        value: RequestFailure;
      };
      expect(result.ok).toBe(false);
      expect(result.value.status).toBe(0);
      expect(result.value.data).toBeUndefined();
      expect(result.value.message).toEqual(expect.any(String));
    }
    await page.unroute("**/api/imap/**");

    await expect
      .poll(() => telemetry.failedLocalRequests.length)
      .toBe(requestBaseline + 2);
    expect(
      telemetry.failedLocalRequests
        .slice(requestBaseline)
        .map((entry) => entry.replace(/: .+$/, "")),
    ).toEqual(["GET /api/imap/", "POST /api/imap/"]);
    telemetry.failedLocalRequests.length = requestBaseline;
    telemetry.consoleErrors.length = consoleBaseline;
  });

  if (!usesNativePromises) {
    return;
  }

  await test.step("native IMAP wrappers do not require jQuery", async () => {
    await page.route("**/api/imap/**", async (route) => {
      if (!isIMAPSettingsRoute(route)) {
        await route.continue();
        return;
      }
      await route.fulfill({
        contentType: "application/json",
        json:
          route.request().method() === "GET"
            ? []
            : { message: "saved", success: true },
        status: 200,
      });
    });
    expect(
      await page.evaluate(async (settings) => {
        const originalDollar = window.$;
        const originalJQuery = window.jQuery;
        window.$ = undefined;
        window.jQuery = undefined;
        try {
          await Promise.all([api.IMAP.get(), api.IMAP.post(settings)]);
          return true;
        } finally {
          window.$ = originalDollar;
          window.jQuery = originalJQuery;
        }
      }, syntheticSettings),
    ).toBe(true);
    await page.unroute("**/api/imap/**");
  });
}

async function assertStoredSecretContract(
  page: Page,
  telemetry: BrowserTelemetry,
): Promise<void> {
  await test.step("stored IMAP password remains write-only", async () => {
    const consoleErrorsBefore = telemetry.consoleErrors.length;
    const failedResponsesBefore = telemetry.failedLocalResponses.length;
    const contract = await page.evaluate(async (settings) => {
      const apiKey = (
        window as Window & {
          user: { api_key: string };
        }
      ).user.api_key;
      const headers = {
        Authorization: `Bearer ${apiKey}`,
        "Content-Type": "application/json",
      };
      const storedSettings = {
        ...settings,
        enabled: false,
        host: "localhost",
      };
      const missingPassword = { ...storedSettings } as Record<string, unknown>;
      delete missingPassword.password;
      const missingCreateResponse = await fetch("/api/imap/", {
        body: JSON.stringify(missingPassword),
        headers,
        method: "POST",
      });
      const createResponse = await fetch("/api/imap/", {
        body: JSON.stringify(storedSettings),
        headers,
        method: "POST",
      });
      const readResponse = await fetch("/api/imap/", { headers });
      const readBody = await readResponse.text();
      const replacementPassword = `${settings.password}-rotated`;
      const replacementResponse = await fetch("/api/imap/", {
        body: JSON.stringify({ ...storedSettings, password: replacementPassword }),
        headers,
        method: "POST",
      });
      const replacementReadResponse = await fetch("/api/imap/", { headers });
      const replacementReadBody = await replacementReadResponse.text();
      const updateResponse = await fetch("/api/imap/", {
        body: JSON.stringify({ ...storedSettings, password: "" }),
        headers,
        method: "POST",
      });
      return {
        createStatus: createResponse.status,
        missingCreateStatus: missingCreateResponse.status,
        password: settings.password,
        replacementPassword,
        replacementReadBody,
        replacementStatus: replacementResponse.status,
        readBody,
        readSettings: JSON.parse(readBody) as Array<Record<string, unknown>>,
        readStatus: readResponse.status,
        updateStatus: updateResponse.status,
      };
    }, syntheticSettings);

    expect(contract.createStatus).toBe(201);
    expect(contract.missingCreateStatus).toBe(500);
    expect(contract.readStatus).toBe(200);
    expect(contract.replacementStatus).toBe(201);
    expect(contract.updateStatus).toBe(201);
    expect(contract.readBody).not.toContain(contract.password);
    expect(contract.replacementReadBody).not.toContain(contract.password);
    expect(contract.replacementReadBody).not.toContain(contract.replacementPassword);
    for (const forbidden of [
      "password_ciphertext",
      "gophishfr-cred:",
      "browser-test-key",
    ]) {
      expect(contract.readBody).not.toContain(forbidden);
      expect(contract.replacementReadBody).not.toContain(forbidden);
    }
    expect(contract.readSettings).toHaveLength(1);
    expect(contract.readSettings[0]).not.toHaveProperty("password");

    await page.locator("#reporttab").click();
    await expect(page.locator("#imaphost")).toHaveValue("localhost");
    await expect(page.locator("#imappassword")).toHaveValue("");

    const exposed = await page.evaluate((passwordValues) => {
      const storageValues = [localStorage, sessionStorage].flatMap((storage) =>
        Array.from({ length: storage.length }, (_, index) => storage.key(index))
          .filter((key): key is string => key !== null)
          .map((key) => storage.getItem(key) ?? ""),
      );
      return {
        dom: passwordValues.some((value) =>
          document.documentElement.outerHTML.includes(value),
        ),
        storage: storageValues.some((storedValue) =>
          passwordValues.some((value) => storedValue.includes(value)),
        ),
      };
    }, [contract.password, contract.replacementPassword, "gophishfr-cred:", "browser-test-key"]);
    expect(exposed).toEqual({ dom: false, storage: false });
    expect(
      telemetry.consoleErrors
        .slice(consoleErrorsBefore)
        .filter((entry) => !entry.includes("status of 500 (Internal Server Error)")),
    ).toEqual([]);
    telemetry.consoleErrors.length = consoleErrorsBefore;
    expect(telemetry.failedLocalResponses.slice(failedResponsesBefore)).toEqual([
      "500 /api/imap/",
    ]);
    telemetry.failedLocalResponses.length = failedResponsesBefore;
  });
}

async function assertLoadConsumer(page: Page, usesNativePromises: boolean): Promise<void> {
  await test.step("IMAP loads preserve fields and reject stale responses", async () => {
    const contract = await page.evaluate(async (nativeMode) => {
      type IMAPSettings = typeof syntheticResponseSettings & { last_login: string };
      type Failure = {
        data?: { message?: string };
        responseJSON?: { message?: string };
      };
      type DualRequest<T> = PromiseLike<T> & {
        done: (callback: (value: T) => void) => DualRequest<T>;
        fail: (callback: (error: Failure) => void) => DualRequest<T>;
        reject: (error: Failure) => void;
        resolve: (value: T) => void;
      };

      const makeRequest = <T>(): DualRequest<T> => {
        const doneCallbacks: Array<(value: T) => void> = [];
        const failCallbacks: Array<(error: Failure) => void> = [];
        let resolvePromise: (value: T) => void = () => undefined;
        let rejectPromise: (error: Failure) => void = () => undefined;
        const promise = new Promise<T>((resolve, reject) => {
          resolvePromise = resolve;
          rejectPromise = reject;
        });
        const request: DualRequest<T> = {
          done(callback) {
            doneCallbacks.push(callback);
            return request;
          },
          fail(callback) {
            failCallbacks.push(callback);
            return request;
          },
          reject(error) {
            if (nativeMode) {
              rejectPromise(error);
            } else {
              failCallbacks.forEach((callback) => callback(error));
            }
          },
          resolve(value) {
            if (nativeMode) {
              resolvePromise(value);
            } else {
              doneCallbacks.forEach((callback) => callback(value));
            }
          },
          then: promise.then.bind(promise),
        };
        return request;
      };

      const originalGet = api.IMAP.get;
      const originalErrorFlash = errorFlash;
      const requests: Array<DualRequest<IMAPSettings[]>> = [];
      const errors: string[] = [];
      api.IMAP.get = (() => {
        const request = makeRequest<IMAPSettings[]>();
        requests.push(request);
        return request;
      }) as typeof api.IMAP.get;
      errorFlash = (message: string) => {
        errors.push(message);
      };

      const first = {
        delete_reported_campaign_email: false,
        enabled: true,
        folder: "Older Reports",
        host: "older-imap.example.invalid",
        ignore_cert_errors: true,
        imap_freq: "120",
        last_login: "2026-09-01T10:00:00Z",
        port: "143",
        restrict_domain: "older.example.invalid",
        tls: false,
        username: "synthetic-older-user",
      };
      const second = {
        delete_reported_campaign_email: true,
        enabled: true,
        folder: "Current Reports",
        host: "current-imap.example.invalid",
        ignore_cert_errors: false,
        imap_freq: "90",
        last_login: "2026-09-02T10:00:00Z",
        port: "993",
        restrict_domain: "example.invalid",
        tls: true,
        username: "synthetic-user",
      };

      document.getElementById("reporttab")?.click();
      document.getElementById("reporttab")?.click();
      requests[1].resolve([second]);
      await Promise.resolve();
      requests[0].resolve([first]);
      await Promise.resolve();

      const value = (id: string) =>
        (document.getElementById(id) as HTMLInputElement).value;
      const checked = (id: string) =>
        (document.getElementById(id) as HTMLInputElement).checked;
      const staleResult = {
        deleteReported: checked("deletecampaign"),
        enabled: checked("use_imap"),
        folder: value("folder"),
        host: value("imaphost"),
        ignoreCertErrors: checked("ignorecerterrors"),
        password: value("imappassword"),
        port: value("imapport"),
        restrictDomain: value("restrictdomain"),
        tls: checked("use_tls"),
        username: value("imapusername"),
      };

      document.getElementById("reporttab")?.click();
      requests[2].resolve([
        {
          ...second,
          folder: "",
          restrict_domain: "",
        },
      ]);
      await Promise.resolve();
      const emptyResult = {
        folder: value("folder"),
        password: value("imappassword"),
        restrictDomain: value("restrictdomain"),
      };

      const retainedHost = "retained-imap.example.invalid";
      (document.getElementById("imaphost") as HTMLInputElement).value = retainedHost;
      (document.getElementById("imappassword") as HTMLInputElement).value =
        "local-only-password";
      document.getElementById("reporttab")?.click();
      requests[3].resolve([]);
      await Promise.resolve();
      const emptyListResult = {
        host: value("imaphost"),
        lastLoginDisplay:
          (document.getElementById("lastlogindiv") as HTMLElement).style.display,
        password: value("imappassword"),
      };

      document.getElementById("reporttab")?.click();
      requests[4].reject({
        data: { message: "synthetic native load failure" },
        responseJSON: { message: "synthetic legacy load failure" },
      });
      await Promise.resolve();
      await Promise.resolve();

      api.IMAP.get = originalGet;
      errorFlash = originalErrorFlash;
      return {
        emptyListResult,
        emptyResult,
        errors,
        requestCount: requests.length,
        staleResult,
      };
    }, usesNativePromises);

    expect(contract.requestCount).toBe(5);
    expect(contract.staleResult).toEqual(
      usesNativePromises
        ? {
            deleteReported: true,
            enabled: true,
            folder: "Current Reports",
            host: "current-imap.example.invalid",
            ignoreCertErrors: false,
            password: "",
            port: "993",
            restrictDomain: "example.invalid",
            tls: true,
            username: "synthetic-user",
          }
        : {
            deleteReported: false,
            enabled: true,
            folder: "Older Reports",
            host: "older-imap.example.invalid",
            ignoreCertErrors: true,
            password: "",
            port: "143",
            restrictDomain: "older.example.invalid",
            tls: false,
            username: "synthetic-older-user",
          },
    );
    expect(contract.emptyResult).toEqual({
      folder: "",
      password: "",
      restrictDomain: "",
    });
    expect(contract.emptyListResult).toEqual({
      host: "retained-imap.example.invalid",
      lastLoginDisplay: "none",
      password: "",
    });
    expect(contract.errors).toEqual(["Error fetching IMAP settings"]);
  });
}

async function assertSaveConsumer(page: Page, usesNativePromises: boolean): Promise<void> {
  await test.step("IMAP saves preserve callback order, retry and cleanup", async () => {
    const contract = await page.evaluate(async ({ nativeMode, settings }) => {
      type Failure = {
        data?: { message?: string };
        responseJSON?: { message?: string };
      };
      type Response = { message: string; success: boolean };
      type DualRequest<T> = PromiseLike<T> & {
        always: (callback: () => void) => DualRequest<T>;
        done: (callback: (value: T) => void) => DualRequest<T>;
        fail: (callback: (error: Failure) => void) => DualRequest<T>;
        reject: (error: Failure) => void;
        resolve: (value: T) => void;
      };

      const events: string[] = [];
      const makeRequest = <T>(): DualRequest<T> => {
        const alwaysCallbacks: Array<() => void> = [];
        const doneCallbacks: Array<(value: T) => void> = [];
        const failCallbacks: Array<(error: Failure) => void> = [];
        let resolvePromise: (value: T) => void = () => undefined;
        let rejectPromise: (error: Failure) => void = () => undefined;
        const promise = new Promise<T>((resolve, reject) => {
          resolvePromise = resolve;
          rejectPromise = reject;
        });
        const request: DualRequest<T> = {
          always(callback) {
            alwaysCallbacks.push(callback);
            return request;
          },
          done(callback) {
            doneCallbacks.push(callback);
            return request;
          },
          fail(callback) {
            failCallbacks.push(callback);
            return request;
          },
          reject(error) {
            if (nativeMode) {
              rejectPromise(error);
            } else {
              failCallbacks.forEach((callback) => callback(error));
              alwaysCallbacks.forEach((callback) => {
                events.push("cleanup");
                callback();
              });
            }
          },
          resolve(value) {
            if (nativeMode) {
              resolvePromise(value);
            } else {
              doneCallbacks.forEach((callback) => callback(value));
              alwaysCallbacks.forEach((callback) => {
                events.push("cleanup");
                callback();
              });
            }
          },
          then: promise.then.bind(promise),
        };
        return request;
      };

      const originalGet = api.IMAP.get;
      const originalPost = api.IMAP.post;
      const originalErrorFlash = errorFlash;
      const originalSuccessFlashFade = successFlashFade;
      const requests: Array<DualRequest<Response>> = [];
      const payloads: Array<typeof settings> = [];
      const errors: string[] = [];
      let bodyScrollWrites = 0;
      let documentScrollWrites = 0;
      Object.defineProperty(document.body, "scrollTop", {
        configurable: true,
        get: () => 0,
        set: () => {
          bodyScrollWrites += 1;
        },
      });
      Object.defineProperty(document.documentElement, "scrollTop", {
        configurable: true,
        get: () => 0,
        set: () => {
          documentScrollWrites += 1;
        },
      });

      api.IMAP.get = (() => {
        events.push("reload");
        const request = makeRequest<never[]>();
        request.resolve([]);
        return request;
      }) as typeof api.IMAP.get;
      api.IMAP.post = ((payload: typeof settings) => {
        payloads.push({ ...payload });
        const request = makeRequest<Response>();
        requests.push(request);
        return request;
      }) as typeof api.IMAP.post;
      errorFlash = (message: string) => {
        errors.push(message);
        events.push("failure");
      };
      successFlashFade = () => {
        events.push("feedback");
        return undefined;
      };

      const setValue = (id: string, value: string) => {
        (document.getElementById(id) as HTMLInputElement).value = value;
      };
      const setChecked = (id: string, checked: boolean) => {
        (document.getElementById(id) as HTMLInputElement).checked = checked;
      };
      setValue("imaphost", settings.host);
      setValue("imapport", settings.port);
      setValue("imapusername", settings.username);
      setValue("imappassword", settings.password);
      setChecked("use_imap", settings.enabled);
      setChecked("use_tls", settings.tls);
      setValue("folder", settings.folder);
      setValue("imapfreq", settings.imap_freq);
      setValue("restrictdomain", settings.restrict_domain);
      setChecked("ignorecerterrors", settings.ignore_cert_errors);
      setChecked("deletecampaign", settings.delete_reported_campaign_email);

      const saveButton = document.getElementById("savesettings") as HTMLButtonElement;
      const firstReturn = saveButton.click();
      saveButton.click();
      requests.forEach((request) =>
        request.resolve({ message: "Successfully saved IMAP settings.", success: true }),
      );
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();

      saveButton.click();
      requests[requests.length - 1].reject({
        data: { message: "synthetic native save failure" },
        responseJSON: { message: "synthetic legacy save failure" },
      });
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();

      saveButton.click();
      requests[requests.length - 1].resolve({
        message: "Successfully saved IMAP settings.",
        success: true,
      });
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();

      setValue("imappassword", "");
      saveButton.click();
      requests[requests.length - 1].resolve({
        message: "Successfully saved IMAP settings.",
        success: true,
      });
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();

      api.IMAP.get = originalGet;
      api.IMAP.post = originalPost;
      errorFlash = originalErrorFlash;
      successFlashFade = originalSuccessFlashFade;
      delete document.body.scrollTop;
      delete document.documentElement.scrollTop;
      return {
        bodyScrollWrites,
        documentScrollWrites,
        errors,
        events,
        firstReturnIsUndefined: firstReturn === undefined,
        payloads,
        requestCount: requests.length,
      };
    }, { nativeMode: usesNativePromises, settings: syntheticSettings });

    expect(contract.firstReturnIsUndefined).toBe(true);
    expect(contract.requestCount).toBe(usesNativePromises ? 4 : 5);
    expect(contract.bodyScrollWrites).toBe(contract.requestCount);
    expect(contract.documentScrollWrites).toBe(contract.requestCount);
    expect(contract.payloads).toHaveLength(usesNativePromises ? 4 : 5);
    expect(contract.payloads[0]).toEqual(syntheticSettings);
    expect(contract.payloads.at(-1)).toEqual({ ...syntheticSettings, password: "" });
    expect(contract.errors).toEqual([
      usesNativePromises
        ? "synthetic native save failure"
        : "synthetic legacy save failure",
    ]);
    expect(contract.events).toEqual(
      usesNativePromises
        ? [
            "feedback",
            "reload",
            "failure",
            "feedback",
            "reload",
            "feedback",
            "reload",
          ]
        : [
            "feedback",
            "reload",
            "cleanup",
            "feedback",
            "reload",
            "cleanup",
            "failure",
            "cleanup",
            "feedback",
            "reload",
            "cleanup",
            "feedback",
            "reload",
            "cleanup",
          ],
    );
    expect(contract.events.filter((event) => event === "cleanup")).toHaveLength(
      usesNativePromises ? 0 : 5,
    );
    expect(contract.events.filter((event) => event === "feedback")).toHaveLength(
      usesNativePromises ? 3 : 4,
    );
    expect(contract.events.filter((event) => event === "reload")).toHaveLength(
      usesNativePromises ? 3 : 4,
    );
  });
}

export async function assertIMAPSettingsContract(
  page: Page,
  telemetry: BrowserTelemetry,
): Promise<void> {
  const usesNativePromises = await page.evaluate(
    () =>
      api.IMAP.get.toString().includes("requestJSON") &&
      api.IMAP.post.toString().includes("requestJSON"),
  );

  await assertStoredSecretContract(page, telemetry);
  await assertTransportContracts(page, telemetry, usesNativePromises);
  await assertLoadConsumer(page, usesNativePromises);
  await assertSaveConsumer(page, usesNativePromises);

  expect(telemetry.pageErrors).toEqual([]);
}
