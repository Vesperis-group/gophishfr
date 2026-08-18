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
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  const failedLocalRequests: string[] = [];
  const failedLocalResponses: string[] = [];
  const unexpectedExternalRequests: string[] = [];
  const loadedAssets = new Set<string>();

  page.on("pageerror", (error) => pageErrors.push(error.message));
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
    expect(
      await page.evaluate(
        () =>
          typeof window.jQuery === "function" &&
          typeof window.jQuery.fn.modal === "function" &&
          typeof window.jQuery.fn.DataTable === "function",
      ),
    ).toBe(true);
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
    await expect(page.getByRole("heading", { name: "Results for Browser Fixture Campaign" })).toBeVisible();
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
    await expect(page.locator("#campaignTable")).toContainText("Browser Fixture Campaign");

    await page.locator('a[href="#archivedCampaigns"]').click();
    await expect(page.locator("#archivedCampaigns")).toHaveClass(/active/);

    await page.getByRole("button", { name: "New Campaign" }).click();
    await expect(page.locator("#modal")).toHaveClass(/in/);
    await expect(page.locator("#modal")).toBeVisible();
    await expect(page.locator(".select2-container")).toHaveCount(4);
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

  await test.step("template editor initializes CKEditor and Bootstrap tabs", async () => {
    await page.getByRole("link", { name: "Email Templates" }).click();
    await expect(page).toHaveURL(/\/templates$/);
    await expect(page.getByRole("heading", { name: "Email Templates" })).toBeVisible();
    await expect(page.locator("#templateTable")).toBeVisible();
    await expect(page.locator("#templateTable")).toContainText(
      "Browser Fixture Template",
    );

    await page.getByRole("button", { name: "New Template" }).click();
    await expect(page.locator("#modal")).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(
          () =>
            typeof window.CKEDITOR === "object" &&
            window.CKEDITOR.instances.html_editor !== undefined,
        ),
      )
      .toBe(true);
    await page.locator('a[href="#html"]').click();
    await expect(page.locator("#html")).toHaveClass(/active/);
    await expect(page.locator(".cke")).toBeVisible();
    await page.locator('#modal .modal-footer button[data-dismiss="modal"]').click();
    await expect(page.locator("#modal")).not.toBeVisible();
  });

  await test.step("password strength bundle resolves zxcvbn", async () => {
    await page.getByRole("link", { name: "User Management" }).click();
    await expect(page).toHaveURL(/\/users$/);
    await expect(page.getByRole("heading", { name: "User Management" })).toBeVisible();

    await page.getByRole("button", { name: "New User" }).click();
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
      "/js/dist/app/templates.min.js",
      "/js/dist/app/passwords.min.js",
      "/js/dist/app/users.min.js",
      "/js/src/vendor/ckeditor/ckeditor.js",
      "/js/src/vendor/ckeditor/adapters/jquery.js",
    ]),
  );
});
