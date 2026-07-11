import { defineConfig, devices } from "@playwright/test"
import fs from "node:fs"
import os from "node:os"
import path from "node:path"

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: [["list"]],
  expect: {
    toHaveScreenshot: {
      // Small tolerance for anti-aliasing differences between runs.
      maxDiffPixelRatio: 0.02,
      animations: "disabled",
    },
  },
  use: {
    baseURL: "http://localhost:4173",
    viewport: { width: 1280, height: 720 },
    deviceScaleFactor: 1,
    locale: "en-US",
    timezoneId: "UTC",
    trace: "on-first-retry",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  webServer: {
    command: "pnpm dev --port 4173 --strictPort",
    url: "http://localhost:4173",
    reuseExistingServer: !process.env.CI,
  },
})
