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

// Minimal shape of a jQuery jqXHR-derived deferred, matching what
// static/js/src/app/gophish.js's query() (a $.ajax() wrapper) returns and
// what static/js/src/app/groups.js consumes via .done()/.fail().
type JQueryDeferredLike<T> = {
  done: (callback: (data: T) => void) => JQueryDeferredLike<T>;
  fail: (callback: (jqXHR: { responseJSON?: { message?: string } }) => void) => JQueryDeferredLike<T>;
};

type GroupSummary = {
  id: number;
  name: string;
};

type GophishGroupsApi = {
  groupId: {
    delete: (id: number) => JQueryDeferredLike<{ message?: string }>;
  };
  groups: {
    get: () => JQueryDeferredLike<GroupSummary[]>;
  };
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

test("Bootstrap 5 frontend smoke", async ({ context, page }) => {
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
    pageErrors.push(error.stack || error.message);
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

  await test.step("login loads Bootstrap 5 CSS and JavaScript", async () => {
    const response = await page.goto("/login");
    expect(response?.status()).toBe(200);
    await expect(page).toHaveTitle("GophishFR - Login");
    await expect(page.locator("form.form-signin")).toBeVisible();
    await expect(page.locator("form.form-signin")).toHaveCSS("max-width", "400px");
    await expect(page.locator('input[name="username"]')).toBeVisible();
    await expect(page.locator('input[name="password"]')).toBeVisible();
    // Login form accessibility: inputs have associated labels
    await expect(page.locator('label[for="username"]')).toHaveCount(1);
    await expect(page.locator('label[for="password"]')).toHaveCount(1);
    await expect(page.locator('#logo')).toHaveAttribute('alt', 'GophishFR logo');
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
          typeof window.jQuery.fn.DataTable === "function",
      ),
    ).toBe(true);
    // Bootstrap jQuery bridge must be absent (data-bs-no-jquery disables it)
    expect(
      await page.evaluate(
        () =>
          typeof window.jQuery.fn.modal !== "function" &&
          typeof window.jQuery.fn.tooltip !== "function" &&
          typeof window.jQuery.fn.collapse !== "function",
      ),
    ).toBe(true);
    // Native Bootstrap 5 API must be available
    expect(
      await page.evaluate(
        () =>
          typeof window.bootstrap === "object" &&
          typeof window.bootstrap.Modal === "function" &&
          typeof window.bootstrap.Tooltip === "function",
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
      .locator(".navbar-gophishfr")
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
    await expect(responsivePage.locator(".navbar-toggler")).toBeVisible();
    await responsivePage.locator(".navbar-toggler").click();
    await expect(responsivePage.locator(".navbar-collapse")).toHaveClass(/show/);
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
    await expect(page.locator("#modal")).toHaveClass(/show/);
    await expect(page.locator("#modal")).toBeVisible();
    await expect(page.locator(".select2-container")).toHaveCount(4);
    // Select2: open and select a known fixture for a single-select (sending profile)
    const profileContainer = page.locator('select#profile + .select2-container, select#profile ~ .select2-container').first();
    await profileContainer.click();
    await expect(page.locator('.select2-dropdown')).toBeVisible();
    await page.locator('.select2-results__option').filter({ hasText: 'Browser Fixture Sending Profile' }).click();
    await expect(profileContainer.locator('.select2-selection__rendered')).toContainText('Browser Fixture Sending Profile');

    // Select2: open and select for a multi-select (groups)
    const groupContainer = page.locator('select#users + .select2-container, select#users ~ .select2-container').first();
    await groupContainer.click();
    await expect(page.locator('.select2-dropdown')).toBeVisible();
    await page.locator('.select2-results__option').filter({ hasText: 'Browser Fixture Group' }).click();

    // Verify dropdown renders inside/above modal without overflow
    const modalRect = await page.locator('#modal .modal-content').boundingBox();
    const dropdownVisible = await page.locator('.select2-dropdown').isVisible().catch(() => false);
    if (dropdownVisible) {
      const dropdownRect = await page.locator('.select2-dropdown').boundingBox();
      if (modalRect && dropdownRect) {
        expect(dropdownRect.x).toBeGreaterThanOrEqual(modalRect.x - 5);
      }
    }
    const urlHelp = page.locator('label[for="url"] [data-bs-toggle="tooltip"]');
    await urlHelp.hover();
    await expect(page.locator(".tooltip.show")).toContainText(
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
    await page.locator('#modal .modal-footer button[data-bs-dismiss="modal"]').click();
  });

  await test.step("group modal and DataTables interaction remain functional", async () => {
    await page.getByRole("link", { name: "Users & Groups" }).click();
    await expect(page).toHaveURL(/\/groups$/);
    await expect(page.getByRole("heading", { name: "Users & Groups" })).toBeVisible();
    await expect(page.locator("#groupTable")).toBeVisible();
    await expect(page.locator("#groupTable")).toContainText("Browser Fixture Group");
    // DataTables BS5 wrapper classes
    await expect(page.locator("#groupTable_wrapper")).toBeVisible();
    await expect(page.locator("#groupTable_filter input")).toHaveClass(/form-control/);
    const paginationClasses = await page.locator("#groupTable_wrapper .pagination").getAttribute("class");
    expect(paginationClasses).toContain("pagination");

    // Exercise search filtering
    await page.locator("#groupTable_filter input").fill("Browser Fixture");
    await expect(page.locator("#groupTable")).toContainText("Browser Fixture Group");
    await page.locator("#groupTable_filter input").fill("XYZNONEXISTENT999");
    await expect(page.locator("#groupTable")).not.toContainText("Browser Fixture Group");
    await page.locator("#groupTable_filter input").fill("");
    await expect(page.locator("#groupTable")).toContainText("Browser Fixture Group");

    // Exercise sortable header
    const nameHeader = page.locator("#groupTable thead th").first();
    const initialClass = await nameHeader.getAttribute("class");
    await nameHeader.click();
    const afterClass = await nameHeader.getAttribute("class");
    expect(afterClass).not.toBe(initialClass);

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
    await page.locator('#modal .modal-footer button[data-bs-dismiss="modal"]').click();
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
    await page.locator('#modal .modal-footer button[data-bs-dismiss="modal"]').click();
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
    await page.locator('#modal .modal-footer button[data-bs-dismiss="modal"]').click();
    await expect(page.locator("#modal")).not.toBeVisible();
  });


  await test.step("datetimepicker opens with Font Awesome icons and toggles panes", async () => {
    await page.getByRole("link", { name: "Campaigns", exact: true }).click();
    await expect(page).toHaveURL(/\/campaigns$/);
    await page.waitForFunction(() => typeof window.bootstrap !== "undefined");
    await page.getByRole("button", { name: "New Campaign" }).click();
    await expect(page.locator("#modal")).toHaveClass(/show/);

    // Click on launch date input to open datetimepicker
    const picker = page.locator(".bootstrap-datetimepicker-widget:visible");
    await expect(async () => {
      if (!(await picker.isVisible())) {
        await page.locator("#launch_date").click();
      }
      await expect(picker).toBeVisible({ timeout: 1_000 });
    }).toPass({ timeout: 5_000 });

    // Verify Font Awesome icons (no glyphicon)
    expect(await picker.locator(".fa").count()).toBeGreaterThan(0);
    expect(await picker.locator("[class*=glyphicon]").count()).toBe(0);

    // Verify date pane is initially visible (collapse show)
    const datePaneVisible = await picker.locator("li.collapse.show .datepicker").count();
    expect(datePaneVisible).toBe(1);

    // Click the toggle action to switch to time pane
    await picker.locator('[data-action="togglePicker"]').click();
    await expect(picker.locator("li.collapse.show .timepicker")).toBeVisible();
    await expect(picker.locator("li.collapse:not(.show) .datepicker")).toHaveCount(1);

    await page.locator('#modal .modal-footer button[data-bs-dismiss="modal"]').click();
    await expect(page.locator("#modal")).not.toBeVisible();
  });

  await test.step("nested modal stacking z-index is correct", async () => {
    await page.getByRole("button", { name: "New Campaign" }).click();
    await expect(page.locator("#modal")).toHaveClass(/show/);

    const nestedTrigger = page.locator('#modal button', { hasText: "Send Test Email" });
    await nestedTrigger.click();
    await expect(page.locator("#sendTestEmailModal")).toHaveClass(/show/);
    await expect(page.locator("#modal")).toHaveClass(/show/);

    // Verify nested modal z-index is above parent while the parent remains visible.
    const zIndexes = await page.evaluate(() => {
      const parent = document.querySelector("#modal") as HTMLElement;
      const child = document.querySelector("#sendTestEmailModal") as HTMLElement;
      const backdrops = document.querySelectorAll(".modal-backdrop");
      const lastBackdrop = backdrops[backdrops.length - 1] as HTMLElement;
      return {
        parentZ: parseInt(parent.style.zIndex || "0", 10),
        childZ: parseInt(child.style.zIndex || "0", 10),
        lastBackdropZ: parseInt(lastBackdrop?.style.zIndex || "0", 10),
      };
    });
    expect(zIndexes.childZ).toBeGreaterThan(zIndexes.parentZ);
    expect(zIndexes.lastBackdropZ).toBeGreaterThan(zIndexes.parentZ);

    // Close nested modal and restore focus to the trigger.
    await page.locator('#sendTestEmailModal .btn-close, #sendTestEmailModal [data-bs-dismiss="modal"]').first().click();
    await expect(page.locator("#sendTestEmailModal")).not.toHaveClass(/show/);
    await expect.poll(() => nestedTrigger.evaluate((el) => document.activeElement === el)).toBe(true);

    // Parent stays open and usable.
    await expect(page.locator("#modal")).toHaveClass(/show/);
    expect(await page.evaluate(() => document.body.classList.contains("modal-open"))).toBe(true);

    // Close parent
    await page.locator('#modal .modal-footer button[data-bs-dismiss="modal"]').click();
    await expect(page.locator("#modal")).not.toBeVisible();
  });

  await test.step("modal accessibility: focus trap and keyboard behavior", async () => {
    await page.getByRole("button", { name: "New Campaign" }).click();
    await expect(page.locator("#modal")).toHaveClass(/show/);

    // Focus should be inside the modal after opening (BS5 moves focus async)
    await expect.poll(() => page.evaluate(() => {
      const modal = document.querySelector("#modal");
      return modal?.contains(document.activeElement) ?? false;
    })).toBe(true);

    await page.keyboard.press("Escape");
    await expect(page.locator("#modal")).toHaveClass(/show/);

    // Close the modal explicitly and verify focus returns to the trigger
    await page.locator('#modal .modal-footer button[data-bs-dismiss="modal"]').click();
    await expect(page.locator("#modal")).not.toBeVisible();

    // Focus should return to the trigger button after modal close
    await expect.poll(() => page.evaluate(() => {
      return document.activeElement?.textContent?.trim() ?? "";
    })).toContain("New Campaign");
  });

  await test.step("layout: no critical overflow and content below navbar", async () => {
    // Desktop check
    const layoutDesktop = await page.evaluate(() => {
      const navbar = document.querySelector(".navbar") as HTMLElement;
      const navbarRect = navbar.getBoundingClientRect();
      const content = document.querySelector(".main") as HTMLElement;
      const contentRect = content?.getBoundingClientRect();
      return {
        navbarBottom: navbarRect.bottom,
        contentTop: contentRect?.top ?? 0,
        bodyScrollWidth: document.body.scrollWidth,
        viewportWidth: window.innerWidth,
      };
    });
    expect(layoutDesktop.contentTop).toBeGreaterThanOrEqual(layoutDesktop.navbarBottom - 2);
    expect(layoutDesktop.bodyScrollWidth).toBeLessThanOrEqual(layoutDesktop.viewportWidth + 5);

    // Sidebar top should be >= navbar bottom on desktop
    const sidebarTop = await page.evaluate(() => {
      const sidebar = document.querySelector('.sidebar') as HTMLElement;
      const navbar = document.querySelector('.navbar') as HTMLElement;
      return {
        sidebarTop: sidebar ? sidebar.getBoundingClientRect().top : null,
        navbarBottom: navbar.getBoundingClientRect().bottom,
      };
    });
    if (sidebarTop.sidebarTop !== null) {
      expect(sidebarTop.sidebarTop).toBeGreaterThanOrEqual(sidebarTop.navbarBottom - 2);
    }

    // Mobile check (use /settings which has no wide DataTable)
    const mobilePage = await context.newPage();
    await mobilePage.setViewportSize({ width: 375, height: 667 });
    await mobilePage.goto("/settings");
    const layoutMobile = await mobilePage.evaluate(() => {
      const navbar = document.querySelector(".navbar") as HTMLElement;
      const navbarRect = navbar.getBoundingClientRect();
      const content = document.querySelector(".main") as HTMLElement;
      const contentRect = content?.getBoundingClientRect();
      return {
        navbarBottom: navbarRect.bottom,
        contentTop: contentRect?.top ?? 0,
        bodyScrollWidth: document.body.scrollWidth,
        viewportWidth: window.innerWidth,
      };
    });
    expect(layoutMobile.contentTop).toBeGreaterThanOrEqual(layoutMobile.navbarBottom - 2);
    expect(layoutMobile.bodyScrollWidth).toBeLessThanOrEqual(layoutMobile.viewportWidth + 5);

    // Modal fits viewport
    await mobilePage.goto("/campaigns");
    await mobilePage.getByRole("button", { name: "New Campaign" }).click();
    await expect(mobilePage.locator("#modal")).toHaveClass(/show/);
    const modalWidth = await mobilePage.evaluate(() => {
      const dialog = document.querySelector(".modal-dialog") as HTMLElement;
      return dialog.getBoundingClientRect().width;
    });
    expect(modalWidth).toBeLessThanOrEqual(375);
    await mobilePage.close();
  });

  await test.step("mobile: routes have visible content without overflow", async () => {
    const mobilePage2 = await context.newPage();
    await mobilePage2.setViewportSize({ width: 375, height: 667 });

    const routes = ["/", "/campaigns", "/templates", "/settings", "/groups"];
    for (const route of routes) {
      await mobilePage2.goto(route);
      const layout = await mobilePage2.evaluate(() => {
        const dataTablesWrapper = document.querySelector(".dataTables_wrapper") as HTMLElement | null;
        return {
          bodyScrollWidth: document.body.scrollWidth,
          viewportWidth: window.innerWidth,
          hasHeading: !!document.querySelector("h1, h2, h3, .page-header, [role='heading']"),
          navbarVisible: document.querySelector(".navbar")?.getBoundingClientRect().height! > 0,
          hasDataTablesWrapper: !!dataTablesWrapper,
          dataTablesWrapperOverflowX: dataTablesWrapper ? window.getComputedStyle(dataTablesWrapper).overflowX : null,
        };
      });
      expect(layout.bodyScrollWidth, `overflow on ${route}`).toBeLessThanOrEqual(layout.viewportWidth + 5);
      if (layout.hasDataTablesWrapper) {
        expect(layout.dataTablesWrapperOverflowX, `DataTables wrapper overflow on ${route}`).toBe("auto");
      }
      expect(layout.hasHeading, `no heading on ${route}`).toBe(true);
      expect(layout.navbarVisible, `navbar hidden on ${route}`).toBe(true);
    }

    // Editor/modal on mobile: modal content reachable and scrollable
    await mobilePage2.goto("/campaigns");
    await mobilePage2.getByRole("button", { name: "New Campaign" }).click();
    await expect(mobilePage2.locator("#modal")).toHaveClass(/show/);
    const modalScroll = await mobilePage2.evaluate(() => {
      const dialog = document.querySelector(".modal-dialog") as HTMLElement;
      const content = document.querySelector(".modal-content") as HTMLElement;
      return {
        dialogWidth: dialog.getBoundingClientRect().width,
        viewportWidth: window.innerWidth,
        contentReachable: content !== null && content.getBoundingClientRect().height > 0,
        dialogScrollable: dialog.classList.contains("modal-dialog-scrollable"),
      };
    });
    expect(modalScroll.dialogWidth).toBeLessThanOrEqual(modalScroll.viewportWidth);
    expect(modalScroll.contentReachable).toBe(true);
    expect(modalScroll.dialogScrollable).toBe(true);

    await mobilePage2.close();
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
    await expect(page.locator("#password-strength-container")).toHaveClass(/d-none/);
    await page.locator("#password").fill("browser fixture password 2026");
    await expect(page.locator("#password-strength-container")).not.toHaveClass(
      /d-none/,
    );
    // Password strength bar should have a bg-* class and numeric aria-valuenow > 0
    const strengthBar = page.locator("#password-strength-bar");
    await expect(strengthBar).toHaveAttribute("aria-valuenow", /^[1-9]\d*$/);
    const barClasses = await strengthBar.getAttribute("class");
    expect(barClasses).toMatch(/bg-(success|warning|danger)/);
    await expect(page.locator("#password-strength-description")).not.toBeEmpty();
    await page.locator('#modal [data-bs-dismiss="modal"]').first().click();
    await expect(page.locator("#modal")).not.toBeVisible();
  });

  await test.step("blueimp CSV file upload: rejected extension and successful CSV import", async () => {
    await page.getByRole("link", { name: "Users & Groups" }).click();
    await expect(page).toHaveURL(/\/groups$/);
    await page.getByRole("button", { name: "New Group" }).click();
    await expect(page.locator("#modal")).toBeVisible();

    // Rejected extension: try uploading a .exe file (should not populate targets)
    const fileInput = page.locator('#modal input[type="file"]');
    const targetsBeforeExe = await page.locator("#targetsTable tbody tr").count();
    await fileInput.setInputFiles({
      name: "malicious.exe",
      mimeType: "application/octet-stream",
      buffer: Buffer.from("MZ fake executable content"),
    });
    // The file upload should not add rows to the target table for invalid extensions
    await page.waitForTimeout(500);
    const targetsAfterExe = await page.locator("#targetsTable tbody tr").count();
    expect(targetsAfterExe).toBe(targetsBeforeExe);

    // Successful CSV import with valid data
    const csvContent = "First Name,Last Name,Email,Position\nCSVTest,User,csvtest@localhost.invalid,Tester\nCSVSecond,Person,csvsecond@localhost.invalid,Dev";
    await fileInput.setInputFiles({
      name: "targets.csv",
      mimeType: "text/csv",
      buffer: Buffer.from(csvContent),
    });
    await expect(page.locator("#targetsTable")).toContainText("csvtest@localhost.invalid");
    await expect(page.locator("#targetsTable")).toContainText("csvsecond@localhost.invalid");
    await expect(page.locator("#targetsTable")).toContainText("CSVTest");

    await page.locator('#modal .modal-footer button[data-bs-dismiss="modal"]').click();
    await expect(page.locator("#modal")).not.toBeVisible();
  });

  await test.step("AJAX save/update path with synthetic fixture data", async () => {
    // Drive the real first-party UI flow on /groups so the group is actually
    // created through api.groups.post(...).done/.fail inside groups.js, not
    // a raw fetch() that bypasses the jQuery AJAX client entirely.
    const groupName = `BrowserTestGroup_${Date.now()}`;

    await page.getByRole("button", { name: "New Group" }).click();
    await expect(page.locator("#modal")).toBeVisible();
    await page.locator("#name").fill(groupName);
    await page.locator("#firstName").fill("Ajax");
    await page.locator("#lastName").fill("Test");
    await page.locator("#email").fill("ajaxtest@localhost.invalid");
    await page.locator("#position").fill("Fixture");
    await page.locator("#targetForm").getByRole("button", { name: "Add" }).click();
    await expect(page.locator("#targetsTable")).toContainText("ajaxtest@localhost.invalid");

    await page.locator("#modalSubmit").click();
    await expect(page.locator("#modal")).not.toBeVisible();
    await expect(page.locator("#groupTable")).toContainText(groupName);

    // Discover the created group's id through the first-party window.api
    // client (the same jqXHR-backed client groups.js uses), wrapping its
    // .done()/.fail() callbacks in a native Promise so Playwright can await it.
    const groupId = await page.evaluate(
      (name) =>
        new Promise<number>((resolve, reject) => {
          const api = (window as Window & { api: GophishGroupsApi }).api;
          api.groups
            .get()
            .done((groups) => {
              const created = groups.find((group) => group.name === name);
              if (created) {
                resolve(created.id);
              } else {
                reject(new Error(`Group "${name}" not found via api.groups.get()`));
              }
            })
            .fail((jqXHR) => {
              reject(new Error(jqXHR.responseJSON?.message ?? "api.groups.get() request failed"));
            });
        }),
      groupName,
    );
    expect(Number.isInteger(groupId)).toBe(true);

    // Cleanup via the same first-party jqXHR-backed client, also wrapped in
    // a native Promise.
    await page.evaluate(
      (id) =>
        new Promise<void>((resolve, reject) => {
          const api = (window as Window & { api: GophishGroupsApi }).api;
          api.groupId
            .delete(id)
            .done(() => resolve())
            .fail((jqXHR) => {
              reject(new Error(jqXHR.responseJSON?.message ?? "api.groupId.delete() request failed"));
            });
        }),
      groupId,
    );
  });

  await test.step("Select2 keyboard search and multi-select behavior", async () => {
    await page.getByRole("link", { name: "Campaigns", exact: true }).click();
    await expect(page).toHaveURL(/\/campaigns$/);
    await page.getByRole("button", { name: "New Campaign" }).click();
    await expect(page.locator("#modal")).toHaveClass(/show/);

    // Select2 keyboard search on the sending profile single-select
    const profileContainer = page.locator('select#profile + .select2-container, select#profile ~ .select2-container').first();
    await profileContainer.click();
    await expect(page.locator('.select2-dropdown')).toBeVisible();
    // Type a search query
    await page.locator('.select2-search__field').fill("Browser Fixture");
    await expect(page.locator('.select2-results__option')).toContainText("Browser Fixture Sending Profile");
    // Select via Enter key
    await page.keyboard.press("Enter");
    await expect(profileContainer.locator('.select2-selection__rendered')).toContainText('Browser Fixture Sending Profile');

    // Multi-select keyboard behavior on groups
    const groupContainer = page.locator('select#users + .select2-container, select#users ~ .select2-container').first();
    await groupContainer.click();
    await expect(page.locator('.select2-dropdown')).toBeVisible();
    await page.locator('.select2-search__field').fill("Browser");
    await expect(page.locator('.select2-results__option')).toContainText("Browser Fixture Group");
    await page.keyboard.press("Enter");
    // Verify the selection appears as a tag in multi-select
    await expect(groupContainer.locator('.select2-selection__choice')).toContainText("Browser Fixture Group");

    await page.locator('#modal .modal-footer button[data-bs-dismiss="modal"]').click();
    await expect(page.locator("#modal")).not.toBeVisible();
  });

  await test.step("DateTimePicker value update and interaction", async () => {
    await page.getByRole("button", { name: "New Campaign" }).click();
    await expect(page.locator("#modal")).toHaveClass(/show/);

    // The launch_date should have a value (auto-populated)
    const initialValue = await page.locator("#launch_date").inputValue();
    expect(initialValue.length).toBeGreaterThan(0);
    // format is "MMMM Do YYYY, h:mm a" (e.g. "June 15th 2024, 2:30 pm"); the
    // datepicker's day cells render the bare day number (see currentDate.date()
    // in static/js/src/vendor/bootstrap-datetime.js), so strip the ordinal
    // suffix to compare against cell text.
    const initialDayMatch = initialValue.match(/\b(\d{1,2})(?:st|nd|rd|th)\b/);
    expect(initialDayMatch).not.toBeNull();
    const initialDay = initialDayMatch === null ? "" : initialDayMatch[1];

    const picker = page.locator(".bootstrap-datetimepicker-widget:visible");
    await expect(async () => {
      if (!(await picker.isVisible())) {
        await page.locator("#launch_date").click();
      }
      await expect(picker).toBeVisible({ timeout: 1_000 });
    }).toPass({ timeout: 5_000 });

    // Click a definitely different current-month day: scan the visible days
    // and pick the first one whose text does not match the currently
    // selected day, instead of a fixed index that could coincide with it (or
    // not exist), so the change-detection assertion always runs.
    const currentMonthDays = picker.locator(".datepicker-days td.day:not(.old):not(.new)");
    const dayCount = await currentMonthDays.count();
    expect(dayCount).toBeGreaterThan(1);
    const dayTexts: string[] = [];
    for (let i = 0; i < dayCount; i += 1) {
      dayTexts.push((await currentMonthDays.nth(i).innerText()).trim());
    }
    const targetIndex = dayTexts.findIndex((text) => text !== initialDay);
    expect(targetIndex).toBeGreaterThanOrEqual(0);

    await currentMonthDays.nth(targetIndex).click();
    const updatedValue = await page.locator("#launch_date").inputValue();
    expect(updatedValue.length).toBeGreaterThan(0);
    // Value must differ from initial: a different day was definitely selected.
    expect(updatedValue).not.toBe(initialValue);

    // Verify the widget data object is functional
    const dpData = await page.evaluate(() => {
      const dp = window.jQuery("#launch_date").data("DateTimePicker");
      return {
        exists: dp !== undefined,
        hasDate: dp?.date() !== undefined && dp?.date() !== null,
      };
    });
    expect(dpData.exists).toBe(true);
    expect(dpData.hasDate).toBe(true);

    await page.locator('#modal .modal-footer button[data-bs-dismiss="modal"]').click();
    await expect(page.locator("#modal")).not.toBeVisible();
  });

  await test.step("jQuery 3.7.1 runtime identity confirmed", async () => {
    const jQueryInfo = await page.evaluate(() => ({
      version: window.jQuery.fn.jquery,
      singleInstance: window.jQuery === window.$,
    }));
    expect(jQueryInfo.version).toBe("3.7.1");
    expect(jQueryInfo.singleInstance).toBe(true);
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
