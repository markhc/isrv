// Integration tests for the home page: real router, real i18n, real
// react-query, with the backend mocked at the network layer by MSW.
import { screen, waitFor } from "@testing-library/react"
import { http, HttpResponse } from "msw"
import { afterEach, describe, expect, it, vi } from "vitest"
import { renderApp } from "@/test/render-app"
import { server } from "@/test/server"

afterEach(() => {
  delete window.__ISRV_CONFIG__
  vi.unstubAllGlobals()
})

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

  it("encrypts before upload and appends the key only to the share fragment", async () => {
    vi.stubGlobal("isSecureContext", true)
    window.__ISRV_CONFIG__ = {
      serverName: "iSRV",
      disableUploadPage: false,
      maxFileSizeMb: 100,
      minAgeDays: 1,
      maxAgeDays: 30,
      e2eeEnabled: true,
      e2eeMaxFileSizeMb: 64,
      version: "test",
    }

    let requestForm: FormData | undefined
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
        requestForm = init?.body as FormData
        return Response.json({
          status: "created",
          filename: "encrypted-file.isrv",
          shortUrl: "http://files.example/d/abc123",
          expiration: "2026-08-01T12:00:00Z",
        })
      })
    )

    const { user, container } = await renderApp("/")
    await user.click(await screen.findByRole("checkbox", { name: /^Encrypt in browser/ }))
    await uploadTestFile(user, container, "private-notes.txt")

    const link = await screen.findByRole("link", {
      name: /http:\/\/files\.example\/d\/abc123#/,
    })
    const shareURL = link.getAttribute("href")!
    const keyToken = shareURL.split("#")[1]
    const uploadedFile = requestForm?.get("file") as File

    expect(keyToken).toMatch(/^[A-Za-z0-9_-]{44}$/)
    expect(requestForm?.get("clientEncryptionVersion")).toBe("1")
    expect(uploadedFile.name).toBe("encrypted-file.isrv")
    expect([...requestForm!.values()]).not.toContain(keyToken)
  })

  it("hides browser encryption when the page is not a secure context", async () => {
    vi.stubGlobal("isSecureContext", false)
    window.__ISRV_CONFIG__ = {
      serverName: "iSRV",
      disableUploadPage: false,
      maxFileSizeMb: 100,
      minAgeDays: 1,
      maxAgeDays: 30,
      e2eeEnabled: true,
      e2eeMaxFileSizeMb: 64,
      version: "test",
    }

    await renderApp("/")

    expect(screen.queryByRole("checkbox", { name: "Encrypt in browser" })).not.toBeInTheDocument()
  })

  it("rejects an oversized browser-encrypted file before encryption", async () => {
    vi.stubGlobal("isSecureContext", true)
    const fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)
    window.__ISRV_CONFIG__ = {
      serverName: "iSRV",
      disableUploadPage: false,
      maxFileSizeMb: 100,
      minAgeDays: 1,
      maxAgeDays: 30,
      e2eeEnabled: true,
      e2eeMaxFileSizeMb: 1,
      version: "test",
    }

    const { user, container } = await renderApp("/")
    await user.click(await screen.findByRole("checkbox", { name: /^Encrypt in browser/ }))
    const input = container.querySelector<HTMLInputElement>("input[type=file]")!
    await user.upload(input, new File([new Uint8Array(1024 * 1024 + 1)], "large.bin"))

    expect(await screen.findByText("Browser-encrypted files are limited to 1 MiB.")).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it("navigates to the FAQ page via the footer", async () => {
    const { user, router } = await renderApp("/")
    await screen.findByRole("heading", { name: "iSRV" })

    await user.click(screen.getByRole("link", { name: "FAQ" }))

    await waitFor(() => expect(router.state.location.pathname).toBe("/faq"))
    expect(await screen.findByText("Frequently asked questions")).toBeInTheDocument()
  })
})
