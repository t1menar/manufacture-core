import { mkdirSync } from "node:fs";
import { join } from "node:path";

import { expect, test, type Page } from "@playwright/test";

// Screenshots for docs/reviews: `MF_SHOTS_DIR=/some/dir npm run shots`.
const dir = process.env.MF_SHOTS_DIR ?? "test-results/shots";
mkdirSync(dir, { recursive: true });

async function settle3d(page: Page, ms = 2500) {
  await expect(page.getByTestId("conveyor-3d").locator("canvas")).toBeVisible();
  await expect(page.getByTestId("scene-stats")).toContainText(/[1-9]\d* fps/, { timeout: 30_000 });
  await page.waitForTimeout(ms); // camera fly-in + items moving
}

for (const [name, url] of [
  ["conveyor-hello-world", "/?pipeline=hello-world&docking=0&warmup=20000"],
  ["conveyor-geo-analysis", "/?pipeline=geo-analysis&docking=0&warmup=30000"],
  ["conveyor-video", "/?pipeline=video&docking=0&warmup=30000"],
  ["conveyor-stress-20", "/?pipeline=stress-20&docking=0&warmup=40000"],
] as const) {
  test(name, async ({ page }) => {
    await page.goto(url);
    await settle3d(page);
    await page.screenshot({ path: join(dir, `${name}.png`) });
  });
}

test("conveyor-docking", async ({ page }) => {
  await page.goto("/?pipeline=geo-analysis");
  await expect(page.getByTestId("conveyor-3d").locator("canvas")).toBeVisible();
  await page.waitForTimeout(1500);
  await page.screenshot({ path: join(dir, "conveyor-docking.png") });
});

test("conveyor-2d-fallback", async ({ page }) => {
  await page.goto("/?pipeline=geo-analysis&view=2d&docking=0&warmup=30000");
  await expect(page.getByTestId("conveyor-2d")).toBeVisible();
  await page.waitForTimeout(800);
  await page.screenshot({ path: join(dir, "conveyor-2d-fallback.png") });
});

test("modules", async ({ page }) => {
  await page.goto("/modules?docking=0&warmup=30000");
  await expect(page.getByTestId("modules-table")).toBeVisible();
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(dir, "modules.png") });
  await page.getByTestId("modules-table").getByRole("link", { name: "geo-analyze" }).click();
  await expect(page.locator("[data-testid^='chart-']").first()).toBeVisible();
  await page.waitForTimeout(1200);
  await page.screenshot({ path: join(dir, "module-detail.png"), fullPage: true });
});

test("pipeline-editor", async ({ page }) => {
  await page.goto("/pipelines/geo-analysis");
  await expect(page.getByTestId("validation-summary")).toBeVisible();
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(dir, "pipeline-editor.png") });
});

test("run-detail", async ({ page }) => {
  await page.goto("/runs?pipeline=geo-analysis&docking=0&warmup=40000");
  await page.getByTestId("runs-table").locator("tbody tr a").first().click();
  await expect(page.getByTestId("step-timeline")).toBeVisible();
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(dir, "run-detail.png"), fullPage: true });
});
