import { expect, test } from "@playwright/test";

const baseURL = requiredEnvironmentVariable("GOPHISHFR_BROWSER_BASE_URL");
const username = requiredEnvironmentVariable("GOPHISHFR_BROWSER_USERNAME");
const password = requiredEnvironmentVariable("GOPHISHFR_BROWSER_PASSWORD");
const localOrigin = new URL(baseURL).origin;
const localServer = new URL(baseURL);

type ChartPoint = {
  campaign_id?: number;
  email?: string;
  message?: string;
  x: number;
  y: number;
};

type CampaignResultsResponse = {
  results: Array<{ reported: boolean; status: string }>;
  timeline: Array<{ email: string; message: string; time: string }>;
};

type TemplateResponse = {
  html: string;
  name: string;
};

type LandingPageResponse = {
  capture_credentials: boolean;
  capture_passwords: boolean;
  html: string;
  name: string;
};

type HTMLEditorHandle = {
  getData: () => string;
};

type HTMLEditorRegistry = {
  get: (id: string) => HTMLEditorHandle | undefined;
};

const seededEmailHTML =
  '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>{{.FirstName}} browser fixture</title><style>.browser-cta{color:#123456!important;background-image:url(https://preview.invalid/background.png)}</style></head><body class="email-body" data-fixture="editor-round-trip"><table role="presentation" style="border-collapse:collapse;width:100%"><tr><td><p>Hello <strong>{{.FirstName}}</strong></p><a id="browser-cta" class="browser-cta" data-rid="{{.RId}}" aria-label="Open for {{.FirstName}}" href="{{.URL}}?rid={{.RId}}">Open</a><img src="https://preview.invalid/pixel.png" onerror="window.__previewHandlerExecuted=true" alt="Tracking pixel"><script>window.__previewScriptExecuted=true</script>{{.Tracker}}</td></tr></table></body></html>';

const editedEmailHTML =
  '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>{{.FirstName}} edited fixture</title><style>.browser-cta{color:#654321!important;background-image:url(https://preview.invalid/background.png)}</style></head><body class="email-body edited" data-fixture="editor-round-trip" data-state="edited"><table role="presentation" style="border-collapse:collapse;width:100%"><tr><td><p>Hello <strong>{{.FirstName}}</strong> {{.LastName}}</p><a id="browser-cta" class="browser-cta" data-rid="{{.RId}}" aria-label="Open for {{.FirstName}}" href="{{.URL}}?rid={{.RId}}">Open safely</a><img src="https://preview.invalid/pixel.png" onerror="window.__previewHandlerExecuted=true" alt="Tracking pixel"><script>window.__previewScriptExecuted=true</script>{{.Tracker}}</td></tr></table></body></html>';

const editedLandingPageHTML =
  '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>{{.FirstName}} edited landing page</title><style>.browser-form{max-width:26rem}</style></head><body class="landing-body" data-fixture="editor-round-trip" data-state="edited"><form class="browser-form" data-purpose="synthetic" action="/must-be-rewritten"><label for="browser-user">User</label><input id="browser-user" name="username" value="{{.FirstName}}"><label for="browser-password">Password</label><input id="browser-password" name="password" type="password"><button type="submit">Continue</button></form></body></html>';

type RenderedChart = {
  canvas: HTMLCanvasElement;
  chartArea: { bottom: number; left: number; right: number; top: number };
  data: {
    datasets: Array<{
      data: Array<number | ChartPoint>;
      pointBackgroundColor?: string[];
    }>;
  };
  getDatasetMeta: (datasetIndex: number) => {
    data: Array<{ getCenterPoint: () => { x: number; y: number } }>;
  };
  options: {
    plugins: {
      title: { text: string };
      zoom: { zoom: { pinch: { enabled: boolean } } };
    };
  };
  scales: {
    x: { max: number; min: number; ticks: Array<{ label?: string | string[] }> };
  };
  tooltip: {
    body: Array<{ lines: string[] }>;
    setActiveElements: (
      elements: Array<{ datasetIndex: number; index: number }>,
      position: { x: number; y: number },
    ) => void;
  };
  update: () => void;
};

type ChartLibrary = {
  getChart: (elementId: string) => RenderedChart | undefined;
};

function requiredEnvironmentVariable(name: string): string {
  const value = process.env[name];
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

test("legacy frontend browser smoke baseline", async ({ context, page }) => {
  test.setTimeout(60_000);

  const sandboxServiceWorkerError =
    "Failed to read the 'serviceWorker' property from 'Navigator': Service worker is disabled because the context is sandboxed and lacks the 'allow-same-origin' flag.";
  const pageErrors: string[] = [];
  const sandboxIsolationErrors: string[] = [];
  const consoleErrors: string[] = [];
  const failedLocalRequests: string[] = [];
  const failedLocalResponses: string[] = [];
  const unexpectedExternalRequests: string[] = [];
  const loadedAssets = new Set<string>();

  page.on("pageerror", (error) => {
    if (error.message === sandboxServiceWorkerError) {
      sandboxIsolationErrors.push(error.message);
      return;
    }
    pageErrors.push(error.message);
  });
  page.on("console", (message) => {
    if (message.type() === "error") {
      consoleErrors.push(message.text());
    }
  });
  page.on("requestfailed", (request) => {
    const url = new URL(request.url());
    if (url.origin === localOrigin) {
      failedLocalRequests.push(
        `${request.method()} ${url.pathname}: ${request.failure()?.errorText}`,
      );
    }
  });
  page.on("response", (response) => {
    const url = new URL(response.url());
    if (url.origin !== localOrigin) {
      return;
    }
    if (response.status() >= 400) {
      failedLocalResponses.push(`${response.status()} ${url.pathname}`);
    }
    if (["script", "stylesheet", "image", "font"].includes(response.request().resourceType())) {
      loadedAssets.add(url.pathname);
    }
  });

  await context.route("**/*", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin === localOrigin || ["data:", "blob:"].includes(url.protocol)) {
      await route.continue();
      return;
    }
    if (
      url.protocol === "https:" &&
      url.hostname === "fonts.googleapis.com" &&
      request.resourceType() === "stylesheet"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "text/css",
        body: "",
      });
      return;
    }
    unexpectedExternalRequests.push(request.url());
    await route.abort("blockedbyclient");
  });
  await context.routeWebSocket("**/*", async (webSocket) => {
    const url = new URL(webSocket.url());
    const expectedProtocol = localServer.protocol === "http:" ? "ws:" : "wss:";
    if (
      url.protocol === expectedProtocol &&
      url.hostname === localServer.hostname &&
      url.port === localServer.port
    ) {
      webSocket.connectToServer();
      return;
    }
    unexpectedExternalRequests.push(webSocket.url());
    await webSocket.close({
      code: 1008,
      reason: "Browser smoke tests block non-local WebSockets",
    });
  });

  await test.step("login loads legacy CSS and JavaScript", async () => {
    const response = await page.goto("/login");
    expect(response?.status()).toBe(200);
    await expect(page).toHaveTitle("GophishFR - Login");
    await expect(page.locator("form.form-signin")).toBeVisible();
    await expect(page.locator("form.form-signin")).toHaveCSS("max-width", "400px");
    await expect(page.locator('input[name="username"]')).toBeVisible();
    await expect(page.locator('input[name="password"]')).toBeVisible();
    await expect(page.locator('input[name="csrf_token"]')).toHaveCount(1);
    const loginPresentation = await page.evaluate(() => {
      const button = getComputedStyle(
        document.querySelector<HTMLButtonElement>('button[type="submit"]')!,
      );
      const input = getComputedStyle(
        document.querySelector<HTMLInputElement>('input[name="username"]')!,
      );
      return {
        buttonBackground: button.backgroundColor,
        buttonColor: button.color,
        buttonHeight: parseFloat(button.height),
        inputBorderStyle: input.borderStyle,
        inputHeight: parseFloat(input.height),
      };
    });
    expect(loginPresentation.buttonBackground).not.toBe("rgba(0, 0, 0, 0)");
    expect(loginPresentation.buttonColor).not.toBe(
      loginPresentation.buttonBackground,
    );
    expect(loginPresentation.buttonHeight).toBeGreaterThanOrEqual(40);
    expect(loginPresentation.inputBorderStyle).toBe("solid");
    expect(loginPresentation.inputHeight).toBeGreaterThanOrEqual(40);
    expect(
      await page.evaluate(
        () =>
          typeof window.jQuery === "function" &&
          typeof window.jQuery.fn.modal === "function" &&
          typeof window.jQuery.fn.DataTable === "function",
      ),
    ).toBe(true);
    expect(
      await page.evaluate(() => ({
        dataTables: window.jQuery.fn.dataTable.version,
        moment: window.moment.version,
        parsedCsv: window.Papa.parse("name\nBrowser Fixture").data[1][0],
        uaParser: window.UAParser.VERSION,
      })),
    ).toEqual({
      dataTables: "1.13.11",
      moment: "2.30.1",
      parsedCsv: "Browser Fixture",
      uaParser: "0.7.41",
    });
  });

  await test.step("synthetic login redirects to the dashboard", async () => {
    await page.locator('input[name="username"]').fill(username);
    await page.locator('input[name="password"]').fill(password);
    await Promise.all([
      page.waitForURL((url) => url.pathname === "/"),
      page.getByRole("button", { name: "Sign in" }).click(),
    ]);
    await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
    await expect(page.locator("#dashboard")).toBeVisible();
    await expect(page.locator("#campaignTable")).toContainText(
      "Browser Fixture Campaign",
    );
    await expect(page.locator("#navbar-dropdown")).toContainText(username);

    const navbarPresentation = await page
      .locator(".navbar-inverse")
      .evaluate((navbar) => {
        const navbarStyle = getComputedStyle(navbar);
        const brandStyle = getComputedStyle(
          navbar.querySelector(".navbar-brand")!,
        );
        return {
          background: navbarStyle.backgroundColor,
          brandColor: brandStyle.color,
        };
      });
    expect(navbarPresentation.background).not.toBe("rgba(0, 0, 0, 0)");
    expect(navbarPresentation.brandColor).not.toBe(
      navbarPresentation.background,
    );

    const responsivePage = await context.newPage();
    await responsivePage.setViewportSize({ width: 480, height: 800 });
    await responsivePage.goto("/");
    await expect(responsivePage.locator(".navbar-toggle")).toBeVisible();
    await responsivePage.locator(".navbar-toggle").click();
    await expect(responsivePage.locator(".navbar-collapse")).toHaveClass(/in/);
    await expect(responsivePage.locator("#navbar-dropdown")).toBeVisible();
    await responsivePage.close();
  });

  await test.step("dashboard charts preserve values, labels, and navigation", async () => {
    const chartIDs = [
      "overview_chart",
      "sent_chart",
      "opened_chart",
      "clicked_chart",
      "submitted_data_chart",
      "email_reported_chart",
    ];
    for (const chartID of chartIDs) {
      await expect(page.locator(`canvas#${chartID}`)).toBeVisible();
    }
    await expect(page.locator("#overview_chart")).toHaveAttribute(
      "aria-label",
      "Phishing Success Overview: 1 campaigns",
    );
    await expect(page.locator("#sent_chart")).toHaveAttribute(
      "aria-label",
      "Email Sent: 1 recipients (100%)",
    );

    const sentChart = await page.evaluate(() => {
      const charts = (window as Window & { Chart: ChartLibrary }).Chart;
      const chart = charts.getChart("sent_chart");
      return {
        title: chart?.options.plugins.title.text,
        values: chart?.data.datasets[0].data,
      };
    });
    expect(sentChart).toEqual({ title: "Email Sent", values: [100, 0] });

    const overviewPoint = await page.evaluate(() => {
      const charts = (window as Window & { Chart: ChartLibrary }).Chart;
      const chart = charts.getChart("overview_chart");
      if (!chart) {
        throw new Error("Overview chart was not initialized");
      }
      const point = chart.getDatasetMeta(0).data[0].getCenterPoint();
      const bounds = chart.canvas.getBoundingClientRect();
      return { x: bounds.left + point.x, y: bounds.top + point.y };
    });
    await page.mouse.click(overviewPoint.x, overviewPoint.y);
    await expect(page).toHaveURL(/\/campaigns\/\d+$/);
  });

  await test.step("campaign result charts preserve timeline and status data", async () => {
    await expect(
      page.getByRole("heading", {
        name: "Results for Browser Fixture Campaign",
      }),
    ).toBeVisible();
    await page.locator("#exportButton").click();
    await expect(page.locator("#exportButton + .dropdown-menu")).toBeVisible();
    await page.locator("#exportButton").click();
    await expect(
      page.locator("#exportButton + .dropdown-menu"),
    ).not.toBeVisible();
    const chartIDs = [
      "timeline_chart",
      "sent_chart",
      "opened_chart",
      "clicked_chart",
      "submitted_data_chart",
      "reported_chart",
    ];
    for (const chartID of chartIDs) {
      await expect(page.locator(`canvas#${chartID}`)).toBeVisible();
    }
    await expect(page.locator("#timeline_chart")).toHaveAttribute(
      "aria-label",
      "Campaign Timeline: 5 events",
    );
    await expect(page.locator("#reported_chart")).toHaveAttribute(
      "aria-label",
      "Email Reported: 1 recipients (100%)",
    );

    const chartData = await page.evaluate(() => {
      const charts = (window as Window & { Chart: ChartLibrary }).Chart;
      const chart = charts.getChart("reported_chart");
      return {
        title: chart?.options.plugins.title.text,
        values: chart?.data.datasets[0].data,
      };
    });
    expect(chartData).toEqual({ title: "Email Reported", values: [100, 0] });

    const tooltipLines = await page.evaluate(() => {
      const charts = (window as Window & { Chart: ChartLibrary }).Chart;
      const chart = charts.getChart("timeline_chart");
      if (!chart) {
        throw new Error("Timeline chart was not initialized");
      }
      chart.tooltip.setActiveElements([{ datasetIndex: 0, index: 0 }], { x: 0, y: 0 });
      chart.update();
      return chart.tooltip.body.flatMap((item) => item.lines);
    });
    expect(tooltipLines).toEqual([
      "Event: Email Sent",
      "Email: fixture@localhost.invalid",
    ]);
    const timelineConfiguration = await page.evaluate(() => {
      const charts = (window as Window & { Chart: ChartLibrary }).Chart;
      const chart = charts.getChart("timeline_chart");
      if (!chart) {
        throw new Error("Timeline chart was not initialized");
      }
      return {
        pinchEnabled: chart.options.plugins.zoom.zoom.pinch.enabled,
        tickLabels: chart.scales.x.ticks.map((tick) => String(tick.label)),
      };
    });
    expect(timelineConfiguration.pinchEnabled).toBe(true);
    expect(timelineConfiguration.tickLabels.some((label) => label.includes(":"))).toBe(
      true,
    );

    const zoomGeometry = await page.evaluate(() => {
      const charts = (window as Window & { Chart: ChartLibrary }).Chart;
      const chart = charts.getChart("timeline_chart");
      if (!chart) {
        throw new Error("Timeline chart was not initialized");
      }
      const bounds = chart.canvas.getBoundingClientRect();
      const width = chart.chartArea.right - chart.chartArea.left;
      const y = bounds.top + (chart.chartArea.top + chart.chartArea.bottom) / 2;
      return {
        initialRange: chart.scales.x.max - chart.scales.x.min,
        start: { x: bounds.left + chart.chartArea.left + width * 0.25, y },
        end: { x: bounds.left + chart.chartArea.left + width * 0.75, y },
      };
    });
    await page.mouse.move(zoomGeometry.start.x, zoomGeometry.start.y);
    await page.mouse.down();
    await page.mouse.move(zoomGeometry.end.x, zoomGeometry.end.y);
    await page.mouse.up();
    await expect(page.locator(".chart-reset-zoom")).toBeVisible();

    const zoomedRange = await page.evaluate(() => {
      const charts = (window as Window & { Chart: ChartLibrary }).Chart;
      const chart = charts.getChart("timeline_chart");
      if (!chart) {
        throw new Error("Timeline chart was not initialized");
      }
      return chart.scales.x.max - chart.scales.x.min;
    });
    expect(zoomedRange).toBeLessThan(zoomGeometry.initialRange);

    await page.getByRole("button", { name: "Reset zoom" }).click();
    await expect(page.locator(".chart-reset-zoom")).toBeHidden();
    const resetRange = await page.evaluate(() => {
      const charts = (window as Window & { Chart: ChartLibrary }).Chart;
      const chart = charts.getChart("timeline_chart");
      if (!chart) {
        throw new Error("Timeline chart was not initialized");
      }
      return chart.scales.x.max - chart.scales.x.min;
    });
    expect(resetRange).toBeCloseTo(zoomGeometry.initialRange, 5);

    await page.route(/\/api\/campaigns\/\d+\/results/, async (route) => {
      const response = await route.fetch();
      const body = (await response.json()) as CampaignResultsResponse;
      body.results[0].status = "Email Opened";
      body.results[0].reported = false;
      body.timeline.push({
        email: "fixture@localhost.invalid",
        message: "Email Opened",
        time: new Date().toISOString(),
      });
      await route.fulfill({ response, json: body });
    });
    const refreshedResults = page.waitForResponse((response) =>
      /\/api\/campaigns\/\d+\/results/.test(new URL(response.url()).pathname),
    );
    await page.getByRole("button", { name: "Refresh" }).click();
    expect((await refreshedResults).status()).toBe(200);
    await expect(page.locator("#reported_chart")).toHaveAttribute(
      "aria-label",
      "Email Reported: 0 recipients (0%)",
    );
    await expect(page.locator("#timeline_chart")).toHaveAttribute(
      "aria-label",
      "Campaign Timeline: 6 events",
    );
    const refreshedChartData = await page.evaluate(() => {
      const charts = (window as Window & { Chart: ChartLibrary }).Chart;
      const reported = charts.getChart("reported_chart");
      const timeline = charts.getChart("timeline_chart");
      return {
        reportedValues: reported?.data.datasets[0].data,
        timelineColors: timeline?.data.datasets[0].pointBackgroundColor,
        timelinePoints: timeline?.data.datasets[0].data.length,
      };
    });
    expect(refreshedChartData).toEqual({
      reportedValues: [0, 100],
      timelineColors: [
        "#1abc9c",
        "#f9bf3b",
        "#F39C12",
        "#f05b4f",
        "#45d6ef",
        "#f9bf3b",
      ],
      timelinePoints: 6,
    });
    await page.unroute(/\/api\/campaigns\/\d+\/results/);
  });

  await test.step("campaign controls initialize Bootstrap and jQuery plugins", async () => {
    await page.getByRole("link", { name: "Campaigns", exact: true }).click();
    await expect(page).toHaveURL(/\/campaigns$/);
    await expect(page.getByRole("heading", { name: "Campaigns" })).toBeVisible();
    await expect(page.locator("#campaignTable")).toContainText(
      "Browser Fixture Campaign",
    );

    await page.locator('a[href="#archivedCampaigns"]').click();
    await expect(page.locator("#archivedCampaigns")).toHaveClass(/active/);

    await page.getByRole("button", { name: "New Campaign" }).click();
    await expect(page.locator("#modal")).toHaveClass(/in/);
    await expect(page.locator("#modal")).toBeVisible();
    await expect(page.locator(".select2-container")).toHaveCount(4);
    const urlHelp = page.locator('label[for="url"] [data-toggle="tooltip"]');
    await urlHelp.hover();
    await expect(page.locator(".tooltip.in")).toContainText(
      "Location of the GophishFR listener",
    );
    expect(
      await page.evaluate(
        () =>
          typeof window.jQuery.fn.select2 === "function" &&
          typeof window.jQuery.fn.datetimepicker === "function" &&
          window.jQuery("#launch_date").data("DateTimePicker") !== undefined,
      ),
    ).toBe(true);
    await page.locator('#modal .modal-footer button[data-dismiss="modal"]').click();
  });

  await test.step("group modal and DataTables interaction remain functional", async () => {
    await page.getByRole("link", { name: "Users & Groups" }).click();
    await expect(page).toHaveURL(/\/groups$/);
    await expect(page.getByRole("heading", { name: "Users & Groups" })).toBeVisible();
    await expect(page.locator("#groupTable")).toBeVisible();
    await expect(page.locator("#groupTable")).toContainText("Browser Fixture Group");

    await page.getByRole("button", { name: "New Group" }).click();
    await expect(page.locator("#modal")).toBeVisible();
    await page.locator("#firstName").fill("Synthetic");
    await page.locator("#lastName").fill("Browser");
    await page.locator("#email").fill("second-fixture@localhost.invalid");
    await page.locator("#position").fill("Test fixture");
    await page.locator("#targetForm").getByRole("button", { name: "Add" }).click();
    await expect(page.locator("#targetsTable")).toContainText(
      "second-fixture@localhost.invalid",
    );
    await page.locator('#modal .modal-footer button[data-dismiss="modal"]').click();
  });

  await test.step("template editor round-trips full-document source HTML", async () => {
    await page.getByRole("link", { name: "Email Templates" }).click();
    await expect(page).toHaveURL(/\/templates$/);
    await expect(page.getByRole("heading", { name: "Email Templates" })).toBeVisible();
    await expect(page.locator("#templateTable")).toBeVisible();
    await expect(page.locator("#templateTable")).toContainText(
      "Browser Fixture Template",
    );

    await page.locator('#templateTable button[onclick^="edit("]').click();
    await expect(page.locator("#modal")).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(
          () => {
            const registry = (
              window as Window & {
                GophishHTMLEditor?: HTMLEditorRegistry;
              }
            ).GophishHTMLEditor;
            return registry?.get("html_editor") !== undefined;
          },
        ),
      )
      .toBe(true);
    await page.locator('a[href="#html"]').click();
    await expect(page.locator("#html")).toHaveClass(/active/);
    await expect(page.locator(".gophish-code-editor .cm-editor")).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(() => {
          const registry = (
            window as Window & {
              GophishHTMLEditor: HTMLEditorRegistry;
            }
          ).GophishHTMLEditor;
          return registry.get("html_editor")?.getData();
        }),
      )
      .toBe(seededEmailHTML);

    const codeEditor = page.locator(".gophish-code-editor .cm-content");
    await codeEditor.fill(editedEmailHTML);
    await codeEditor.press("Control+End");
    await codeEditor.press("Enter");
    await codeEditor.pressSequentially("{{.Fir");
    await expect(page.locator(".cm-tooltip-autocomplete")).toBeVisible();
    await expect(page.locator(".cm-tooltip-autocomplete")).toContainText(
      "{{.FirstName}}",
    );
    await page
      .locator(".cm-tooltip-autocomplete [role='option']")
      .filter({ hasText: "{{.FirstName}}" })
      .click();
    expect(
      await page.evaluate(() => {
        const registry = (
          window as Window & {
            GophishHTMLEditor: HTMLEditorRegistry;
          }
        ).GophishHTMLEditor;
        return registry.get("html_editor")?.getData().endsWith("{{.FirstName}}");
      }),
    ).toBe(true);
    await codeEditor.fill(editedEmailHTML);

    await test.step("sandboxed preview is inert and leaves source unchanged", async () => {
      await page.locator('[data-editor-view="preview"]').click();
      const preview = page.locator("iframe[data-editor-preview]");
      await expect(preview).toBeVisible();
      await expect(preview).toHaveAttribute("sandbox", "");
      await expect(preview).toHaveAttribute("referrerpolicy", "no-referrer");
      const previewDocument = page.frameLocator("iframe[data-editor-preview]");
      await expect(previewDocument.locator("body")).toContainText("Open safely");
      await expect(previewDocument.locator("script")).toHaveCount(0);
      await expect(previewDocument.locator("[onerror]")).toHaveCount(0);
      await expect(previewDocument.locator("img[src]")).toHaveCount(0);
      await expect(previewDocument.locator("a[href]")).toHaveCount(0);
      await expect(previewDocument.locator("form:not([inert])")).toHaveCount(0);
      expect(
        await previewDocument.locator("body").evaluate(() => ({
          handler: (window as Window & { __previewHandlerExecuted?: boolean })
            .__previewHandlerExecuted,
          script: (window as Window & { __previewScriptExecuted?: boolean })
            .__previewScriptExecuted,
        })),
      ).toEqual({ handler: undefined, script: undefined });
      expect(sandboxIsolationErrors.length).toBeGreaterThan(0);
      await expect
        .poll(() =>
          page.evaluate(() => {
            const registry = (
              window as Window & {
                GophishHTMLEditor: HTMLEditorRegistry;
              }
            ).GophishHTMLEditor;
            return registry.get("html_editor")?.getData();
          }),
        )
        .toBe(editedEmailHTML);
    });

    await page.locator('[data-editor-view="source"]').click();
    await page.getByRole("button", { name: "Save Template" }).click();
    await expect(page.locator("#modal")).not.toBeVisible();

    const templates = await page.evaluate(async () => {
      const apiKey = (
        window as Window & {
          user: { api_key: string };
        }
      ).user.api_key;
      const response = await fetch("/api/templates/", {
        headers: { Authorization: `Bearer ${apiKey}` },
      });
      if (!response.ok) {
        throw new Error(`Template API returned ${response.status}`);
      }
      return (await response.json()) as TemplateResponse[];
    });
    expect(
      templates.find(({ name }) => name === "Browser Fixture Template")?.html,
    ).toBe(editedEmailHTML);

    await page.locator('#templateTable button[onclick^="edit("]').click();
    await expect(page.locator("#modal")).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(() => {
          const registry = (
            window as Window & {
              GophishHTMLEditor: HTMLEditorRegistry;
            }
          ).GophishHTMLEditor;
          return registry.get("html_editor")?.getData();
        }),
      )
      .toBe(editedEmailHTML);
    await page.locator('#modal .modal-footer button[data-dismiss="modal"]').click();
    await expect(page.locator("#modal")).not.toBeVisible();
  });

  await test.step("landing page editor preserves HTML while backend applies form policy", async () => {
    await page.getByRole("link", { name: "Landing Pages", exact: true }).click();
    await expect(page).toHaveURL(/\/landing_pages$/);
    await expect(page.locator("#pagesTable")).toContainText(
      "Browser Fixture Landing Page",
    );

    await page.locator('#pagesTable button[onclick^="edit("]').click();
    await expect(page.locator("#modal")).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(
          () => {
            const registry = (
              window as Window & {
                GophishHTMLEditor?: HTMLEditorRegistry;
              }
            ).GophishHTMLEditor;
            return registry?.get("html_editor") !== undefined;
          },
        ),
      )
      .toBe(true);
    await expect
      .poll(() =>
        page.evaluate(() => {
          const registry = (
            window as Window & {
              GophishHTMLEditor: HTMLEditorRegistry;
            }
          ).GophishHTMLEditor;
          return registry.get("html_editor")?.getData();
        }),
      )
      .toContain('data-purpose="synthetic"');
    await page.locator(".gophish-code-editor .cm-content").fill(
      editedLandingPageHTML,
    );
    await page.locator('[data-editor-view="preview"]').click();
    const landingPreview = page.frameLocator("iframe[data-editor-preview]");
    await expect(landingPreview.locator("form[inert]")).toHaveCount(1);
    await expect(landingPreview.locator("form[action]")).toHaveCount(0);
    await expect(landingPreview.locator("input:not([disabled])")).toHaveCount(0);
    await expect
      .poll(() =>
        page.evaluate(() => {
          const registry = (
            window as Window & {
              GophishHTMLEditor: HTMLEditorRegistry;
            }
          ).GophishHTMLEditor;
          return registry.get("html_editor")?.getData();
        }),
      )
      .toBe(editedLandingPageHTML);
    await page.locator('[data-editor-view="source"]').click();
    await page.getByRole("button", { name: "Save Page" }).click();
    await expect(page.locator("#modal")).not.toBeVisible();

    const landingPages = await page.evaluate(async () => {
      const apiKey = (
        window as Window & {
          user: { api_key: string };
        }
      ).user.api_key;
      const response = await fetch("/api/pages/", {
        headers: { Authorization: `Bearer ${apiKey}` },
      });
      if (!response.ok) {
        throw new Error(`Landing page API returned ${response.status}`);
      }
      return (await response.json()) as LandingPageResponse[];
    });
    const landingPage = landingPages.find(
      ({ name }) => name === "Browser Fixture Landing Page",
    );
    expect(landingPage).toMatchObject({
      capture_credentials: true,
      capture_passwords: false,
    });
    expect(landingPage?.html).toContain("<!DOCTYPE html>");
    expect(landingPage?.html).toContain('data-state="edited"');
    expect(landingPage?.html).toContain("{{.FirstName}}");
    expect(landingPage?.html).toContain('<form class="browser-form"');
    expect(landingPage?.html).toContain('action=""');
    expect(landingPage?.html).toContain('name="username"');
    expect(landingPage?.html).not.toContain('name="password"');
    expect(landingPage?.html).toContain(".browser-form{max-width:26rem}");

    await page.locator('#pagesTable button[onclick^="edit("]').click();
    await expect(page.locator("#modal")).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(() => {
          const registry = (
            window as Window & {
              GophishHTMLEditor: HTMLEditorRegistry;
            }
          ).GophishHTMLEditor;
          return registry.get("html_editor")?.getData();
        }),
      )
      .toBe(landingPage?.html);
    await page.locator('#modal .modal-footer button[data-dismiss="modal"]').click();
    await expect(page.locator("#modal")).not.toBeVisible();
  });

  await test.step("password strength bundle resolves zxcvbn", async () => {
    await page.getByRole("link", { name: "User Management" }).click();
    await expect(page).toHaveURL(/\/users$/);
    await expect(page.getByRole("heading", { name: "User Management" })).toBeVisible();
    await expect(page.locator("#userTable")).toBeVisible();
    await expect(page.locator("#userTable")).toContainText(username);

    await page.getByRole("button", { name: "New User" }).click();
    expect({ consoleErrors, pageErrors }).toEqual({
      consoleErrors: [],
      pageErrors: [],
    });
    await expect(page.locator("#modal")).toBeVisible();
    await page.locator("#password").fill("browser fixture password 2026");
    await expect(page.locator("#password-strength-container")).not.toHaveClass(
      /hidden/,
    );
    await expect(page.locator("#password-strength-description")).not.toBeEmpty();
  });

  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);
  expect(failedLocalRequests).toEqual([]);
  expect(failedLocalResponses).toEqual([]);
  expect(unexpectedExternalRequests).toEqual([]);
  expect([...loadedAssets]).toEqual(
    expect.arrayContaining([
      "/css/dist/gophish.css",
      "/js/dist/vendor.min.js",
      "/js/dist/app/gophish.min.js",
      "/js/dist/app/charts.min.js",
      "/js/dist/app/dashboard.min.js",
      "/js/dist/app/campaign_results.min.js",
      "/js/dist/app/campaigns.min.js",
      "/js/dist/app/groups.min.js",
      "/js/dist/app/html_editor.min.js",
      "/js/dist/app/templates.min.js",
      "/js/dist/app/passwords.min.js",
      "/js/dist/app/users.min.js",
    ]),
  );
});
