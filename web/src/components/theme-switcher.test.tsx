// UI-state tests: theme selection must be reflected in the rendered UI, the
// document root class and persisted storage.
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeAll, describe, expect, it } from "vitest"
import i18n, { initI18n } from "@/i18n"
import { ThemeSwitcher } from "./theme-switcher"

beforeAll(async () => {
  if (!i18n.isInitialized) await initI18n()
})

describe("ThemeSwitcher", () => {
  it("applies and persists dark mode", async () => {
    const user = userEvent.setup()
    render(<ThemeSwitcher />)

    await user.click(screen.getByRole("button", { name: "Dark" }))

    expect(document.documentElement).toHaveClass("dark")
    expect(window.localStorage.getItem("isrv-theme-mode")).toBe("dark")
  })

  it("switches back to light mode", async () => {
    const user = userEvent.setup()
    render(<ThemeSwitcher />)

    await user.click(screen.getByRole("button", { name: "Dark" }))
    await user.click(screen.getByRole("button", { name: "Light" }))

    expect(document.documentElement).not.toHaveClass("dark")
    expect(window.localStorage.getItem("isrv-theme-mode")).toBe("light")
  })

  it("highlights the active mode button", async () => {
    const user = userEvent.setup()
    render(<ThemeSwitcher />)

    const darkButton = screen.getByRole("button", { name: "Dark" })
    await user.click(darkButton)

    expect(darkButton.className).toContain("bg-primary")
    expect(screen.getByRole("button", { name: "Light" }).className).not.toContain("bg-primary")
  })
})
