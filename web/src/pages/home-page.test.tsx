// Integration tests for the home page: real router, real i18n, real
// react-query, with the backend mocked at the network layer by MSW.
import { screen, waitFor } from "@testing-library/react"
import { http, HttpResponse } from "msw"
import { describe, expect, it } from "vitest"
import { renderApp } from "@/test/render-app"
import { server } from "@/test/server"

async function uploadTestFile(
  user: Awaited<ReturnType<typeof renderApp>>["user"],
  container: HTMLElement,
  name = "notes.txt"
) {
  const input = container.querySelector<HTMLInputElement>("input[type=file]")
  expect(input).not.toBeNull()
  const file = new File(["hello world"], name, { type: "text/plain" })
  await user.upload(input!, file)
  return file
}

describe("home page", () => {
  it("renders the title, subtitle and footer navigation", async () => {
    await renderApp("/")

    expect(await screen.findByRole("heading", { name: "iSRV" })).toBeInTheDocument()
    expect(
      screen.getByText("A temporary file sharing service with a focus on privacy and security.")
    ).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "FAQ" })).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "About" })).toBeInTheDocument()
    expect(screen.getByRole("link", { name: "Privacy" })).toBeInTheDocument()
  })

  it("uploads a file and shows the shareable link with expiration", async () => {
    const { user, container } = await renderApp("/")
    await screen.findByRole("heading", { name: "iSRV" })

    await uploadTestFile(user, container)

    const link = await screen.findByRole("link", {
      name: "http://files.example/d/abc123",
    })
    expect(link).toHaveAttribute("href", "http://files.example/d/abc123")
    expect(screen.getByText("notes.txt")).toBeInTheDocument()
    expect(screen.getByText(/^Expires /)).toBeInTheDocument()
  })

  it("copies the shareable link to the clipboard", async () => {
    const { user, container } = await renderApp("/")
    await screen.findByRole("heading", { name: "iSRV" })

    await uploadTestFile(user, container)
    await screen.findByRole("link", { name: "http://files.example/d/abc123" })

    await user.click(screen.getByRole("button", { name: "Copy" }))

    expect(await screen.findByRole("button", { name: "Copied!" })).toBeInTheDocument()
    expect(await window.navigator.clipboard.readText()).toBe(
      "http://files.example/d/abc123"
    )
  })

  it("shows the server error in a toast when the upload fails", async () => {
    server.use(
      http.post("/", () => HttpResponse.text("file too large", { status: 413 }))
    )

    const { user, container } = await renderApp("/")
    await screen.findByRole("heading", { name: "iSRV" })

    await uploadTestFile(user, container)

    expect(await screen.findByText("file too large")).toBeInTheDocument()
    // No result card is rendered on failure.
    expect(
      screen.queryByRole("link", { name: /files\.example/ })
    ).not.toBeInTheDocument()
  })

  it("navigates to the FAQ page via the footer", async () => {
    const { user, router } = await renderApp("/")
    await screen.findByRole("heading", { name: "iSRV" })

    await user.click(screen.getByRole("link", { name: "FAQ" }))

    await waitFor(() => expect(router.state.location.pathname).toBe("/faq"))
    expect(await screen.findByText("Frequently asked questions")).toBeInTheDocument()
  })
})
