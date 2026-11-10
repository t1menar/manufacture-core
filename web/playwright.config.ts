import { defineConfig, devices } from "@playwright/test";

const PORT = Number(process.env.MF_E2E_PORT ?? 4174);
const SHOTS_DIR = process.env.MF_SHOTS_DIR ?? "test-results/shots";

// WebGL in headless Chromium goes through SwiftShader (software GL) so the 3D scene renders in CI.
const gl = ["--use-angle=swiftshader", "--enable-unsafe-swiftshader", "--ignore-gpu-blocklist"];

export default defineConfig({
  testDir: "e2e",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: `http://127.0.0.1:${String(PORT)}`,
    trace: "retain-on-failure",
    timezoneId: "Europe/Stockholm",
    locale: "ru-RU",
    colorScheme: "dark",
  },
  metadata: { shotsDir: SHOTS_DIR },
  projects: [
    {
      name: "smoke",
      testMatch: /smoke\.spec\.ts/,
      use: { ...devices["Desktop Chrome"], launchOptions: { args: gl } },
    },
    {
      name: "shots",
      testMatch: /shots\.spec\.ts/,
      use: { ...devices["Desktop Chrome"], viewport: { width: 1600, height: 900 }, deviceScaleFactor: 1, launchOptions: { args: gl } },
    },
  ],
  webServer: {
    command: `npx vite preview --mode mock --outDir dist-mock --host 127.0.0.1 --port ${String(PORT)} --strictPort`,
    url: `http://127.0.0.1:${String(PORT)}`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
});
