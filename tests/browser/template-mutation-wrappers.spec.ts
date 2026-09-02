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

type TemplatePayload = {
  attachments: Array<{
    content: string;
    name: string;
    type: string;
  }>;
  envelope_sender: string;
  html: string;
  id?: number;
  name: string;
  subject: string;
  text: string;
};

const createdHTML =
  '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Template &amp; entities</title><style>.card{color:#123456}</style></head><body class="mail-body" data-purpose="template-round-trip"><section class="card" style="padding: 4px" data-state="created"><a href="{{.URL}}" data-rid="{{.RId}}">A &amp; B</a><span title="Apostrophe \' and &quot;quotes&quot;">Hello {{.FirstName}}</span></section></body></html>';
const updatedHTML =
  '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Updated &amp; exact</title><style>.card{color:#654321}</style></head><body class="mail-body updated" data-purpose="template-round-trip" data-state="updated"><main><section class="card" style="padding: 8px"><strong>{{.FirstName}} {{.LastName}}</strong><a href="{{.URL}}" data-rid="{{.RId}}">Updated &amp; safe</a></section></main></body></html>';
const importedHTML =
  '<!doctype html><html><body><article id="synthetic-import-marker" class="imported" data-source="email"><a href="{{.URL}}">Imported &amp; exact</a><script>window.__importExecuted=true</script></article></body></html>';

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

async function runWrapperInvocations(
  page: Page,
  createTemplate: TemplatePayload,
  updateTemplate: TemplatePayload & { id: number },
  importRequest: { content: string; convert_links: boolean },
  withoutJQuery = false,
): Promise<unknown[]> {
  return page.evaluate(
    async ({ createPayload, importPayload, updatePayload, withoutJQuery }) => {
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
        import_email: (request: typeof importPayload) => Request;
        templateId: {
          delete: (id: number) => Request;
          put: (template: typeof updatePayload) => Request;
        };
        templates: {
          post: (template: typeof createPayload) => Request;
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
          ["templates.post", globals.api.templates.post(createPayload)],
          ["templateId.put", globals.api.templateId.put(updatePayload)],
          ["templateId.delete", globals.api.templateId.delete(updatePayload.id)],
          ["import_email", globals.api.import_email(importPayload)],
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
      createPayload: createTemplate,
      importPayload: importRequest,
      updatePayload: updateTemplate,
      withoutJQuery,
    },
  );
}

async function wrappersUseNativeTransport(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const api = (
      window as unknown as {
        api: {
          import_email: () => unknown;
          templateId: { delete: () => unknown; put: () => unknown };
          templates: { post: () => unknown };
        };
      }
    ).api;
    return [
      api.templates.post,
      api.templateId.put,
      api.templateId.delete,
      api.import_email,
    ].every((wrapper) => wrapper.toString().includes("requestJSON"));
  });
}

async function openTemplateModal(page: Page, name: string): Promise<void> {
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

async function closeTemplateModal(page: Page): Promise<void> {
  await page.locator('#modal .modal-footer [data-bs-dismiss="modal"]').click();
  await expect(page.locator("#modal")).toBeHidden();
}

async function setHTML(page: Page, html: string): Promise<void> {
  await page.locator('a[href="#html"]').click();
  await page.locator(".gophish-code-editor .cm-content").fill(html);
  await expect.poll(() => getHTML(page)).toBe(html);
}

async function setText(page: Page, text: string): Promise<void> {
  await page.locator("#text_editor").evaluate((element, value) => {
    const textarea = element as HTMLTextAreaElement;
    textarea.value = value;
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  }, text);
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
  const isTemplateMutation =
    (request.method() === "POST" && url.pathname === "/api/templates/") ||
    (["PUT", "DELETE"].includes(request.method()) &&
      /^\/api\/templates\/\d+$/.test(url.pathname));
  if (!isTemplateMutation && url.pathname !== "/api/import/email") {
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

test("template mutations and email import preserve legacy contracts", async ({ page }) => {
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

  const wrapperCreate: TemplatePayload = {
    attachments: [
      {
        content: "c3ludGhldGljLWF0dGFjaG1lbnQ=",
        name: "synthetic.txt",
        type: "text/plain",
      },
    ],
    envelope_sender: "",
    html: createdHTML,
    name: "",
    subject: 'Wrapper "subject"',
    text: "",
  };
  const wrapperUpdate = { ...wrapperCreate, id: 108 };
  const wrapperImport = {
    content: "Subject: wrapper import\r\n\r\nSynthetic body",
    convert_links: false,
  };
  const wrapperRequests: CapturedRequest[] = [];
  let wrapperFailure = false;
  const wrapperRoute = async (route: Route) => {
    wrapperRequests.push(captureRequest(route));
    await route.fulfill({
      body: JSON.stringify(
        wrapperFailure
          ? { message: "synthetic template wrapper failure" }
          : { success: true },
      ),
      contentType: "application/json",
      status: wrapperFailure ? 500 : 200,
    });
  };
  await page.route("**/api/templates/**", wrapperRoute);
  await page.route("**/api/import/email", wrapperRoute);
  const nativeWrappers = await wrappersUseNativeTransport(page);

  expect(
    await runWrapperInvocations(
      page,
      wrapperCreate,
      wrapperUpdate,
      wrapperImport,
      nativeWrappers,
    ),
  ).toEqual(
    ["templates.post", "templateId.put", "templateId.delete", "import_email"].map(
      (name) => ({
        data: { success: true },
        name,
        settlement: "success",
      }),
    ),
  );
  const expectedWrapperRequests: CapturedRequest[] = [
    {
      authorizationIsBearer: true,
      body: JSON.stringify(wrapperCreate),
      contentType: "application/json",
      method: "POST",
      pathname: "/api/templates/",
      search: "",
    },
    {
      authorizationIsBearer: true,
      body: JSON.stringify(wrapperUpdate),
      contentType: "application/json",
      method: "PUT",
      pathname: "/api/templates/108",
      search: "",
    },
    {
      authorizationIsBearer: true,
      body: "{}",
      contentType: "application/json",
      method: "DELETE",
      pathname: "/api/templates/108",
      search: "",
    },
    {
      authorizationIsBearer: true,
      body: JSON.stringify(wrapperImport),
      contentType: "application/json",
      method: "POST",
      pathname: "/api/import/email",
      search: "",
    },
  ];
  expect(wrapperRequests).toHaveLength(expectedWrapperRequests.length);
  expect(wrapperRequests).toEqual(expect.arrayContaining(expectedWrapperRequests));

  wrapperRequests.length = 0;
  wrapperFailure = true;
  expect(
    await runWrapperInvocations(page, wrapperCreate, wrapperUpdate, wrapperImport),
  ).toEqual(
    ["templates.post", "templateId.put", "templateId.delete", "import_email"].map(
      (name) => ({
        data: { message: "synthetic template wrapper failure" },
        name,
        settlement: "failure",
        status: 500,
      }),
    ),
  );
  expect(wrapperRequests).toHaveLength(4);
  await page.unroute("**/api/templates/**", wrapperRoute);
  await page.unroute("**/api/import/email", wrapperRoute);

  await page.route("**/api/templates/**", (route) => route.abort("failed"));
  await page.route("**/api/import/email", (route) => route.abort("failed"));
  expect(
    await runWrapperInvocations(page, wrapperCreate, wrapperUpdate, wrapperImport),
  ).toEqual(
    ["templates.post", "templateId.put", "templateId.delete", "import_email"].map(
      (name) => ({
        data: undefined,
        name,
        settlement: "failure",
        status: 0,
      }),
    ),
  );
  await page.unroute("**/api/templates/**");
  await page.unroute("**/api/import/email");

  await page.goto("/templates");
  await expect(page.locator("#templateTable")).toBeVisible();

  await openTemplateModal(page, "New Template");
  const parentReturns = await page.evaluate(() => {
    type Pending = {
      done: () => Pending;
      fail: () => Pending;
      then: () => Pending;
    };
    type API = {
      import_email: () => Pending;
      templateId: { delete: () => Pending };
      templates: { post: () => Pending };
    };
    const globals = window as unknown as {
      Swal: { fire: () => Promise<never> };
      api: API;
    };
    const originalImport = globals.api.import_email;
    const originalPost = globals.api.templates.post;
    const originalDelete = globals.api.templateId.delete;
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
      globals.api.import_email = () => pending;
      globals.api.templates.post = () => pending;
      globals.api.templateId.delete = () => pending;
      globals.Swal.fire = () => new Promise(() => {});
      window.eval('templates = [{ id: 108, name: "Synthetic template" }]');
      document.getElementById("email_content")!.textContent =
        "Subject: synthetic\r\n\r\nBody";
      return {
        deleteIsUndefined: window.eval("deleteTemplate(0)") === undefined,
        importIsUndefined: window.eval("importEmail()") === undefined,
        saveIsUndefined: window.eval("save(-1)") === undefined,
      };
    } finally {
      globals.api.import_email = originalImport;
      globals.api.templates.post = originalPost;
      globals.api.templateId.delete = originalDelete;
      globals.Swal.fire = originalFire;
    }
  });
  expect(parentReturns).toEqual({
    deleteIsUndefined: true,
    importIsUndefined: true,
    saveIsUndefined: true,
  });
  await page.reload();
  await expect(page.locator("#templateTable")).toBeVisible();

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

  let releaseSupersededCreate: () => void = () => {};
  const supersededCreateGate = new Promise<void>((resolve) => {
    releaseSupersededCreate = resolve;
  });
  await page.route(
    "**/api/templates/",
    async (route) => {
      await supersededCreateGate;
      await route.fulfill({
        body: JSON.stringify({ message: "superseded template create failure" }),
        contentType: "application/json",
        status: 500,
      });
    },
    { times: 1 },
  );
  await openTemplateModal(page, "New Template");
  await page.locator("#name").fill("Superseded template create");
  await page.evaluate(() => {
    const submit = document.querySelector<HTMLButtonElement>(
      "#modal #modalSubmit",
    );
    submit?.click();
    submit?.click();
  });
  const createRequestCount = () =>
    capturedMutations.filter(
      (request) =>
        request.method === "POST" && request.pathname === "/api/templates/",
    ).length;
  await expect.poll(createRequestCount).toBe(1);
  await expect(page.locator("#modal #modalSubmit")).toBeDisabled();
  await closeTemplateModal(page);
  await openTemplateModal(page, "New Template");
  await expect(page.locator("#modal #modalSubmit")).toBeDisabled();
  await page.evaluate(() => {
    document
      .querySelector<HTMLButtonElement>("#modal #modalSubmit")
      ?.click();
  });
  expect(createRequestCount()).toBe(1);
  releaseSupersededCreate();
  await expect(page.locator("#modal #modalSubmit")).toBeEnabled();
  await expect(page.locator('[id="modal.flashes"]').first()).not.toContainText(
    "superseded template create failure",
  );

  const templateName = `TemplateMutationBaseline_${Date.now()}`;
  const templateSubject = 'Created "subject" & exact';
  const templateText = "Created plaintext\nwith an empty line\n";
  await page.locator("#name").fill(templateName);
  await page.locator("#envelope-sender").fill("");
  await page.locator("#subject").fill(templateSubject);
  await setText(page, templateText);
  await page.locator("#use_tracker_checkbox").uncheck({ force: true });
  await setHTML(page, createdHTML);
  await page.evaluate(() => {
    const globals = window as unknown as {
      FileReader: typeof FileReader;
      releaseAttachmentRead?: () => void;
    };
    const nativeFileReader = globals.FileReader;
    class DeferredFileReader {
      onerror: ((event: Event) => void) | null = null;
      onload: ((event: Event) => void) | null = null;
      result: string | null = null;

      readAsDataURL(file: File) {
        globals.releaseAttachmentRead = () => {
          globals.FileReader = nativeFileReader;
          this.result = `data:${file.type};base64,c3ludGhldGljIGF0dGFjaG1lbnQ=`;
          this.onload?.(new Event("load"));
        };
      }
    }
    globals.FileReader = DeferredFileReader as unknown as typeof FileReader;
  });
  await page.locator("#attachmentUpload").setInputFiles({
    name: "round-trip.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("synthetic attachment"),
  });
  await expect(page.locator("#modal #modalSubmit")).toBeDisabled();
  await page.evaluate(() => {
    document
      .querySelector<HTMLButtonElement>("#modal #modalSubmit")
      ?.click();
  });
  expect(createRequestCount()).toBe(1);
  await page.evaluate(() => {
    (
      window as unknown as {
        releaseAttachmentRead?: () => void;
      }
    ).releaseAttachmentRead?.();
  });
  await expect(page.locator("#modal #modalSubmit")).toBeEnabled();
  await expect(page.locator("#attachmentsTable")).toContainText("round-trip.txt");

  await page.route(
    "**/api/templates/",
    (route) =>
      route.fulfill({
        body: JSON.stringify({ message: "synthetic template create failure" }),
        contentType: "application/json",
        status: 400,
      }),
    { times: 1 },
  );
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator('[id="modal.flashes"]').first()).toContainText(
    "synthetic template create failure",
  );
  await expect(page.locator("#name")).toHaveValue(templateName);
  await expect(page.locator("#subject")).toHaveValue(templateSubject);
  await expect(page.locator("#text_editor")).toHaveValue(templateText);
  await expect.poll(() => getHTML(page)).toBe(createdHTML);
  await expect(page.locator("#attachmentsTable")).toContainText("round-trip.txt");

  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeHidden();
  const createdRow = page
    .locator("#templateTable tbody tr")
    .filter({ hasText: templateName });
  await expect(createdRow).toBeVisible();

  const createRequests = capturedMutations.filter(
    (request) =>
      request.method === "POST" && request.pathname === "/api/templates/",
  );
  expect(createRequests).toHaveLength(3);
  expect(createRequests[2].body).toBe(createRequests[1].body);
  const createBody = JSON.parse(createRequests[1].body ?? "") as TemplatePayload;
  expect(createBody).toEqual({
    attachments: [
      {
        content: Buffer.from("synthetic attachment").toString("base64"),
        name: "round-trip.txt",
        type: "text/plain",
      },
    ],
    envelope_sender: "",
    html: createdHTML,
    name: templateName,
    subject: templateSubject,
    text: templateText,
  });

  const editHandler =
    (await createdRow.locator('button[onclick^="edit("]').getAttribute("onclick")) ??
      "";
  const indexMatch = editHandler.match(/\d+/);
  if (!indexMatch) {
    throw new Error(`Unable to determine template index from ${editHandler}`);
  }
  const templateIndex = Number(indexMatch[0]);
  const templateId = await page.evaluate(
    (index) =>
      (
        window as unknown as {
          templates: Array<{ id: number }>;
        }
      ).templates[index].id,
    templateIndex,
  );

  await createdRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#name")).toHaveValue(templateName);
  await expect(page.locator("#subject")).toHaveValue(templateSubject);
  await expect.poll(() => getHTML(page)).toBe(createdHTML);

  const updatedName = `${templateName}_updated`;
  const updatedSubject = "Updated subject";
  const updatedText = "Updated plaintext";
  await page.locator("#name").fill(updatedName);
  await page.locator("#subject").fill(updatedSubject);
  await setText(page, updatedText);
  await setHTML(page, updatedHTML);
  await page.evaluate(() => {
    (
      window as unknown as {
        templates: unknown[];
      }
    ).templates.reverse();
  });

  let releaseSupersededUpdate: () => void = () => {};
  const supersededUpdateGate = new Promise<void>((resolve) => {
    releaseSupersededUpdate = resolve;
  });
  await page.route(
    (url) => url.pathname === `/api/templates/${templateId}`,
    async (route) => {
      await supersededUpdateGate;
      await route.continue();
    },
    { times: 1 },
  );
  await page.evaluate(() => {
    const submit = document.querySelector<HTMLButtonElement>(
      "#modal #modalSubmit",
    );
    submit?.click();
    submit?.click();
  });
  const updateRequestCount = () =>
    capturedMutations.filter(
      (request) =>
        request.method === "PUT" &&
        request.pathname === `/api/templates/${templateId}`,
    ).length;
  await expect.poll(updateRequestCount).toBe(1);
  await page.evaluate(() => {
    (
      window as unknown as {
        templates: unknown[];
      }
    ).templates.reverse();
  });
  await expect(page.locator("#modal #modalSubmit")).toBeDisabled();
  expect(
    await page
      .locator("#modal .gophish-code-editor")
      .evaluate((element) => (element as HTMLElement).inert),
  ).toBe(true);
  await page.evaluate(() => {
    document
      .querySelector<HTMLElement>("#attachmentsTable .fa-trash-o")
      ?.click();
  });
  await expect(page.locator("#attachmentsTable")).toContainText("round-trip.txt");
  await closeTemplateModal(page);
  await createdRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator("#name")).toHaveValue(templateName);
  await expect(page.locator("#modal #modalSubmit")).toBeDisabled();
  await page.route(
    (url) => url.pathname === "/api/templates/" && url.search === "?{}",
    (route) =>
      route.fulfill({
        body: JSON.stringify({ message: "synthetic template reload failure" }),
        contentType: "application/json",
        status: 500,
      }),
    { times: 1 },
  );
  releaseSupersededUpdate();
  await expect(page.locator("#modal #modalSubmit")).toBeEnabled();
  expect(
    await page
      .locator("#modal .gophish-code-editor")
      .evaluate((element) => (element as HTMLElement).inert),
  ).toBe(false);
  await expect(page.locator("#name")).toHaveValue(updatedName);
  await expect(page.locator("#subject")).toHaveValue(updatedSubject);
  await expect(page.locator("#text_editor")).toHaveValue(updatedText);
  await expect.poll(() => getHTML(page)).toBe(updatedHTML);

  await page.route(
    (url) => url.pathname === `/api/templates/${templateId}`,
    (route) =>
      route.fulfill({
        body: JSON.stringify({ message: "synthetic template update failure" }),
        contentType: "application/json",
        status: 400,
      }),
    { times: 1 },
  );
  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect(page.locator('[id="modal.flashes"]').first()).toContainText(
    "synthetic template update failure",
  );
  await expect(page.locator("#name")).toHaveValue(updatedName);
  await expect(page.locator("#subject")).toHaveValue(updatedSubject);
  await expect(page.locator("#text_editor")).toHaveValue(updatedText);
  await expect.poll(() => getHTML(page)).toBe(updatedHTML);

  await page.locator("#modal #modalSubmit").click();
  await expect(page.locator("#modal")).toBeHidden();
  const updatedRow = page
    .locator("#templateTable tbody tr")
    .filter({ hasText: updatedName });
  await expect(updatedRow).toBeVisible();

  const updateRequests = capturedMutations.filter(
    (request) =>
      request.method === "PUT" &&
      request.pathname === `/api/templates/${templateId}`,
  );
  expect(updateRequests).toHaveLength(3);
  expect(updateRequests[2].body).toBe(updateRequests[1].body);
  expect(JSON.parse(updateRequests[1].body ?? "")).toEqual({
    attachments: createBody.attachments,
    envelope_sender: "",
    html: updatedHTML,
    id: templateId,
    name: updatedName,
    subject: updatedSubject,
    text: updatedText,
  });

  const storedTemplates = await page.evaluate(async () => {
    const globals = window as unknown as { user: { api_key: string } };
    const response = await fetch("/api/templates/", {
      headers: { Authorization: `Bearer ${globals.user.api_key}` },
    });
    if (!response.ok) {
      throw new Error(`Template API returned ${response.status}`);
    }
    return (await response.json()) as TemplatePayload[];
  });
  expect(
    storedTemplates.find((template) => template.id === templateId)?.html,
  ).toBe(updatedHTML);

  await updatedRow.locator('button[onclick^="edit("]').click();
  await expect(page.locator("#modal")).toBeVisible();
  await expect.poll(() => getHTML(page)).toBe(updatedHTML);
  await page
    .locator('#modal button[onclick^="bsModalShow(\'#importEmailModal\'"]')
    .click();
  await expect(page.locator("#importEmailModal")).toBeVisible();
  const rawEmail =
    "From: sender@localhost.invalid\nTo: recipient@localhost.invalid\nSubject: Imported source\nContent-Type: text/plain; charset=utf-8\n\nSynthetic import body";
  await page.locator("#email_content").fill(rawEmail);
  await page.locator("#convert_links_checkbox").uncheck({ force: true });

  const importRequestCount = () =>
    capturedMutations.filter(
      (request) => request.pathname === "/api/import/email",
    ).length;
  let releaseSupersededImport: () => void = () => {};
  const supersededImportGate = new Promise<void>((resolve) => {
    releaseSupersededImport = resolve;
  });
  await page.route(
    "**/api/import/email",
    async (route) => {
      await supersededImportGate;
      await route.fulfill({
        body: JSON.stringify({
          html: importedHTML,
          subject: "Superseded imported subject",
          text: "Superseded imported plaintext",
        }),
        contentType: "application/json",
        status: 200,
      });
    },
    { times: 1 },
  );
  await page.evaluate(() => {
    const submit = document.querySelector<HTMLButtonElement>(
      "#importEmailModal #modalSubmit",
    );
    submit?.click();
    submit?.click();
  });
  await expect.poll(importRequestCount).toBe(1);
  await expect(page.locator("#importEmailModal #modalSubmit")).toBeDisabled();
  await page
    .locator('#importEmailModal .modal-footer [data-bs-dismiss="modal"]')
    .click();
  await expect(page.locator("#importEmailModal")).toBeHidden();
  await page
    .locator('#modal button[onclick^="bsModalShow(\'#importEmailModal\'"]')
    .click();
  await expect(page.locator("#importEmailModal")).toBeVisible();
  await expect(page.locator("#importEmailModal #modalSubmit")).toBeDisabled();
  releaseSupersededImport();
  await expect(page.locator("#importEmailModal #modalSubmit")).toBeEnabled();
  await expect(
    page.locator('#importEmailModal [id="modal.flashes"]'),
  ).not.toContainText("Superseded imported subject");
  await expect(page.locator("#subject")).toHaveValue(updatedSubject);
  await expect(page.locator("#text_editor")).toHaveValue(updatedText);
  await expect.poll(() => getHTML(page)).toBe(updatedHTML);
  await page.locator("#email_content").fill(rawEmail);
  await page.locator("#convert_links_checkbox").uncheck({ force: true });

  await page.route(
    "**/api/import/email",
    (route) =>
      route.fulfill({
        body: JSON.stringify({ message: "synthetic email import failure" }),
        contentType: "application/json",
        status: 500,
      }),
    { times: 1 },
  );
  await page.locator("#importEmailModal #modalSubmit").click();
  await expect(page.locator("#importEmailModal")).toBeVisible();
  await expect(
    page.locator('#importEmailModal [id="modal.flashes"]'),
  ).toContainText("synthetic email import failure");
  await expect(page.locator("#subject")).toHaveValue(updatedSubject);
  await expect(page.locator("#text_editor")).toHaveValue(updatedText);
  await expect.poll(() => getHTML(page)).toBe(updatedHTML);

  await page.route(
    "**/api/import/email",
    (route) =>
      route.fulfill({
        body: JSON.stringify({
          html: importedHTML,
          subject: "Imported subject",
          text: "Imported plaintext",
        }),
        contentType: "application/json",
        status: 200,
      }),
    { times: 1 },
  );
  await page.locator("#importEmailModal #modalSubmit").click();
  await expect(page.locator("#importEmailModal")).toBeHidden();
  await expect(page.locator("#subject")).toHaveValue("Imported subject");
  await expect(page.locator("#text_editor")).toHaveValue("Imported plaintext");
  await expect.poll(() => getHTML(page)).toBe(importedHTML);
  await expect(page.locator("#synthetic-import-marker")).toHaveCount(0);
  const preview = page.frameLocator("iframe[data-editor-preview]");
  await expect(preview.locator("#synthetic-import-marker")).toBeVisible();
  await expect(preview.locator("script")).toHaveCount(0);
  expect(
    await preview.locator("body").evaluate(
      () => (window as unknown as { __importExecuted?: boolean }).__importExecuted,
    ),
  ).toBeUndefined();
  await closeTemplateModal(page);

  const importRequests = capturedMutations.filter(
    (request) => request.pathname === "/api/import/email",
  );
  expect(importRequests).toHaveLength(3);
  expect(importRequests[2].body).toBe(importRequests[1].body);
  expect(JSON.parse(importRequests[1].body ?? "")).toEqual({
    content: rawEmail,
    convert_links: false,
  });

  const deleteCount = () =>
    capturedMutations.filter(
      (request) =>
        request.method === "DELETE" &&
        request.pathname === `/api/templates/${templateId}`,
    ).length;
  await updatedRow.locator("button.btn-danger").click();
  await expect(page.locator(".swal2-popup")).toContainText("Are you sure?");
  await page.locator(".swal2-cancel").click();
  await expect(page.locator(".swal2-popup")).toBeHidden();
  expect(deleteCount()).toBe(0);
  await expect(updatedRow).toBeVisible();

  let releaseFailedDelete: () => void = () => {};
  const failedDeleteGate = new Promise<void>((resolve) => {
    releaseFailedDelete = resolve;
  });
  await page.route(
    (url) => url.pathname === `/api/templates/${templateId}`,
    async (route) => {
      await failedDeleteGate;
      await route.fulfill({
        body: JSON.stringify({ message: "synthetic template delete failure" }),
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
  releaseFailedDelete();
  await expect(page.locator(".swal2-popup")).toBeVisible();
  await expect(page.locator(".swal2-popup")).toContainText(
    "synthetic template delete failure",
  );
  await expect(updatedRow).toBeVisible();
  await page.evaluate(() => {
    (window as unknown as { Swal: { close: () => void } }).Swal.close();
  });
  await expect(page.locator(".swal2-popup")).toBeHidden();

  await updatedRow.locator("button.btn-danger").click();
  await page.evaluate(() => {
    (
      window as unknown as {
        templates: unknown[];
      }
    ).templates.reverse();
  });
  await page.locator(".swal2-confirm").click();
  await expect(page.locator(".swal2-popup")).toContainText("Template Deleted!");
  expect(deleteCount()).toBe(2);
  await Promise.all([
    page.waitForNavigation({ waitUntil: "load" }),
    page.getByRole("button", { name: "OK" }).click(),
  ]);
  await expect(page.locator("#templateTable")).not.toContainText(updatedName);

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
  if (nativeWrappers) {
    expect(relevantPageErrors).toEqual([]);
  } else {
    expect(
      relevantPageErrors.some((message) =>
        message.includes("synthetic template delete failure"),
      ),
    ).toBe(true);
  }
  expect(externalRequests).toEqual([]);
});
