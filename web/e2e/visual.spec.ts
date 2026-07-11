// Visual regression suite. Each test pins the app into a deterministic state
// (mocked API responses, fixed theme/language, UTC timezone from the config)
// and compares a screenshot against the committed baseline. A failing test
// means the page's appearance changed: inspect the diff under test-results/
// and, when the change is intentional, refresh baselines with
// `pnpm test:e2e:update`.
import { expect, test, type Page } from "@playwright/test"

const FILES = [
  {
    id: "f1",
    name: "readme.txt",
    size: 2048,
    contentType: "text/plain",
    expiration: "2026-08-01T12:00:00Z",
    downloads: 3,
    encryptionVersion: 1,
    ipAddress: "10.0.0.1",
    createdAt: "2026-07-01T09:30:00Z",
  },
  {
    id: "f2",
    name: "photo.jpg",
    size: 5 * 1024 * 1024,
    contentType: "image/jpeg",
    expiration: "2026-08-02T12:00:00Z",
    downloads: 0,
    encryptionVersion: 0,
    ipAddress: "10.0.0.2",
    createdAt: "2026-07-02T10:00:00Z",
  },
  {
    id: "f3",
    name: "archive.zip",
    size: 120 * 1024 * 1024,
    contentType: "application/zip",
    expiration: "2026-08-03T12:00:00Z",
    downloads: 12,
    encryptionVersion: 2,
    ipAddress: "10.0.0.1",
    createdAt: "2026-07-03T11:15:00Z",
  },
]

async function mockAdminApi(page: Page, { authenticated = false } = {}) {
  await page.route("**/admin/api/session", (route) =>
    route.fulfill({
      json: authenticated
        ? { authenticated: true, username: "admin" }
        : { authenticated: false },
    }))
  await page.route("**/admin/api/files**", (route) =>
    route.fulfill({ json: { items: FILES, total: FILES.length, limit: 25, offset: 0 } }))
  await page.route("**/d/**", (route) =>
    route.fulfill({ body: "hello from readme", contentType: "text/plain" }))
}

function useTheme(page: Page, mode: "light" | "dark") {
  return page.addInitScript((m) => {
    window.localStorage.setItem("isrv-theme-mode", m)
    window.localStorage.setItem("i18n-lang", "en")
  }, mode)
}

test.describe("home page", () => {
  test("light mode", async ({ page }) => {
    await useTheme(page, "light")
    await page.goto("/")
    await expect(page.getByRole("heading", { name: "iSRV" })).toBeVisible()
    await expect(page).toHaveScreenshot("home-light.png", { fullPage: true })
  })

  test("dark mode", async ({ page }) => {
    await useTheme(page, "dark")
    await page.goto("/")
    await expect(page.getByRole("heading", { name: "iSRV" })).toBeVisible()
    await expect(page).toHaveScreenshot("home-dark.png", { fullPage: true })
  })

  test("upload success state", async ({ page }) => {
    await useTheme(page, "light")
    await page.route("**/", (route) => {
      if (route.request().method() !== "POST") return route.continue()
      return route.fulfill({
        json: {
          status: "created",
          filename: "notes.txt",
          shortUrl: "http://files.example/d/abc123",
          expiration: "2026-08-01T12:00:00Z",
        },
      })
    })
    await page.goto("/")
    await page
      .locator("input[type=file]")
      .setInputFiles({ name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("hello") })
    await expect(page.getByRole("link", { name: "http://files.example/d/abc123" })).toBeVisible()
    await expect(page).toHaveScreenshot("home-upload-success.png", { fullPage: true })
  })
})

test.describe("static pages", () => {
  test("faq page", async ({ page }) => {
    await useTheme(page, "light")
    await page.goto("/faq")
    await expect(page.getByRole("heading", { name: "FAQ", exact: true })).toBeVisible()
    await expect(page).toHaveScreenshot("faq.png", { fullPage: true })
  })

  test("about page", async ({ page }) => {
    await useTheme(page, "light")
    await page.goto("/about")
    await expect(page.locator("h1")).toBeVisible()
    await expect(page).toHaveScreenshot("about.png", { fullPage: true })
  })
})

test.describe("admin", () => {
  test("login form", async ({ page }) => {
    await useTheme(page, "light")
    await mockAdminApi(page, { authenticated: false })
    await page.goto("/admin")
    await expect(page.getByRole("heading", { name: "Administrator Sign In" })).toBeVisible()
    await expect(page).toHaveScreenshot("admin-login.png", { fullPage: true })
  })

  test("files dashboard with detail panel", async ({ page }) => {
    await useTheme(page, "light")
    await mockAdminApi(page, { authenticated: true })
    await page.goto("/admin")
    await expect(page.getByText("Showing 1-3 of 3")).toBeVisible()

    await page.getByText("readme.txt").first().click()
    await expect(page.getByText("hello from readme")).toBeVisible()

    await expect(page).toHaveScreenshot("admin-dashboard.png", { fullPage: true })
  })

  test("delete confirmation dialog", async ({ page }) => {
    await useTheme(page, "light")
    await mockAdminApi(page, { authenticated: true })
    await page.goto("/admin")
    await page.getByText("readme.txt").first().click()
    await page.getByRole("button", { name: "Delete" }).click()
    await expect(page.getByRole("alertdialog")).toBeVisible()

    await expect(page).toHaveScreenshot("admin-delete-dialog.png", { fullPage: true })
  })
})
