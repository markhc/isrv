import { screen } from "@testing-library/react"
import { http, HttpResponse } from "msw"
import { afterEach, describe, expect, it, vi } from "vitest"
import { encryptFile } from "@/features/e2ee/crypto"
import { renderApp } from "@/test/render-app"
import { server } from "@/test/server"

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe("encrypted download page", () => {
  it("fetches without the fragment, decrypts, and saves with private metadata", async () => {
    vi.stubGlobal("isSecureContext", true)
    const original = new File(["private contents"], "private.txt", { type: "text/plain" })
    const encrypted = await encryptFile(original)
    const encryptedBytes = await encrypted.ciphertext.arrayBuffer()
    let requestedURL = ""
    server.use(
      http.get("/api/e2ee/files/:id/content", ({ request }) => {
        requestedURL = request.url
        return new HttpResponse(encryptedBytes, {
          headers: {
            "Content-Length": String(encryptedBytes.byteLength),
            "Content-Type": "application/octet-stream",
          },
        })
      })
    )

    let savedBlob: Blob | undefined
    vi.spyOn(URL, "createObjectURL").mockImplementation((blob) => {
      if (blob instanceof Blob) {
        savedBlob = blob
      }
      return "blob:test"
    })
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined)
    let savedName = ""
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      savedName = this.download
    })

    const { user } = await renderApp(`/d/e2ee-file#${encrypted.keyToken}`)
    await user.click(await screen.findByRole("button", { name: "Decrypt and download" }))

    expect(await screen.findByText("Download started: private.txt")).toBeInTheDocument()
    expect(new URL(requestedURL).hash).toBe("")
    expect(requestedURL).not.toContain(encrypted.keyToken)
    expect(savedName).toBe("private.txt")
    expect(savedBlob?.type).toBe("text/plain")
    expect(await savedBlob?.text()).toBe("private contents")
  })

  it("does not fetch when the fragment key is malformed", async () => {
    vi.stubGlobal("isSecureContext", true)
    let requests = 0
    server.use(
      http.get("/api/e2ee/files/:id/content", () => {
        requests += 1
        return HttpResponse.text("unexpected")
      })
    )

    const { user } = await renderApp("/d/e2ee-file#not+a+key")
    await user.click(await screen.findByRole("button", { name: "Decrypt and download" }))

    expect(await screen.findByText(
      "The decryption key in this link is malformed or unsupported."
    )).toBeInTheDocument()
    expect(requests).toBe(0)
  })

  it("explains that a fragment key is required", async () => {
    vi.stubGlobal("isSecureContext", true)
    await renderApp("/d/e2ee-file")

    expect(await screen.findByText(
      "This link is missing its decryption key. Ask the sender for the complete link."
    )).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Decrypt and download" })).toBeDisabled()
  })

  it("reports authentication failures without creating a plaintext download", async () => {
    vi.stubGlobal("isSecureContext", true)
    const first = await encryptFile(new File(["first"], "first.txt"))
    const second = await encryptFile(new File(["second"], "second.txt"))
    server.use(
      http.get("/api/e2ee/files/:id/content", async () => new HttpResponse(
        await first.ciphertext.arrayBuffer(),
        { headers: { "Content-Type": "application/octet-stream" } }
      ))
    )
    const createObjectURL = vi.spyOn(URL, "createObjectURL")

    const { user } = await renderApp(`/d/e2ee-file#${second.keyToken}`)
    await user.click(await screen.findByRole("button", { name: "Decrypt and download" }))

    expect(await screen.findByText(
      "The key is wrong or the encrypted file is damaged."
    )).toBeInTheDocument()
    expect(createObjectURL).not.toHaveBeenCalled()
  })

  it("does not enable decryption outside a secure context", async () => {
    vi.stubGlobal("isSecureContext", false)
    await renderApp("/d/e2ee-file#key")

    expect(await screen.findByText(
      "This browser or connection cannot securely decrypt the file."
    )).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Decrypt and download" })).toBeDisabled()
  })
})
