// @vitest-environment jsdom
import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it, vi, beforeAll } from "vitest"
import HomePage from "./home-page.tsx"

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { language: "en" },
  }),
}))

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...props }: React.ComponentProps<"a">) => <a {...props}>{children}</a>,
}))

vi.mock("@/components/theme-switcher", () => ({
  ThemeSwitcher: () => <div data-testid="theme-switcher" />,
}))


function wrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>
}

beforeAll(() => {
  Object.assign(window, { location: { origin: "http://localhost" } })
})

describe("HomePage", () => {
  it("renders the app title", () => {
    render(<HomePage />, { wrapper })
    expect(screen.getByRole("heading", { name: /isrv/i })).toBeDefined()
  })

  it("renders the theme switcher", () => {
    render(<HomePage />, { wrapper })
    expect(screen.getAllByTestId("theme-switcher").length).toBeGreaterThan(0)
  })
})
