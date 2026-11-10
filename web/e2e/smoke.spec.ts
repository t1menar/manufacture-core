import { expect, test } from "@playwright/test";

// Runs against `npm run build:mock` (in-browser simulator, no core needed).

test.beforeEach(({ page }) => {
  page.on("pageerror", (e) => {
    throw e;
  });
});

test("3D conveyor renders and items move", async ({ page }) => {
  await page.goto("/?pipeline=hello-world&docking=0&warmup=15000");
  await expect(page.getByTestId("conveyor-3d").locator("canvas")).toBeVisible();
  await expect(page.getByTestId("scene-stats")).toContainText(/[1-9]\d* fps/, { timeout: 20_000 });
  await expect(page.getByTestId("live-indicator")).toHaveAttribute("data-state", "open");
});

test("2D fallback", async ({ page }) => {
  await page.goto("/?pipeline=geo-analysis&view=2d&docking=0");
  await expect(page.getByTestId("conveyor-2d")).toBeVisible();
});

test("pipelines → editor validates", async ({ page }) => {
  await page.goto("/pipelines");
  await page.getByTestId("pipelines-table").getByRole("link", { name: "geo-analysis" }).click();
  await expect(page.getByTestId("validation-summary")).toBeVisible();
});

test("runs → run detail with timeline", async ({ page }) => {
  await page.goto("/runs?pipeline=hello-world&docking=0&warmup=20000");
  const first = page.getByTestId("runs-table").locator("tbody tr a").first();
  await first.click();
  await expect(page.getByTestId("step-timeline")).toBeVisible();
  await expect(page.getByTestId("json-view").first()).toBeVisible();
});

test("modules list and detail charts", async ({ page }) => {
  await page.goto("/modules");
  await page.getByTestId("modules-table").getByRole("link", { name: "geo-analyze" }).click();
  await expect(page.getByText("geo-analyze").first()).toBeVisible();
  await expect(page.locator("[data-testid^='chart-']").first()).toBeVisible();
});

test("bad token is rejected, login recovers", async ({ page }) => {
  await page.goto("/settings");
  await page.evaluate(() => {
    localStorage.setItem("mf-admin-token", "bad");
  });
  await page.goto("/runs");
  await expect(page.getByText("нет доступа")).toBeVisible();
  await page.getByLabel("Admin-токен").fill("mfa_mock_admin_token");
  await page.getByRole("button", { name: "Войти" }).click();
  await expect(page.getByTestId("runs-table")).toBeVisible();
});
