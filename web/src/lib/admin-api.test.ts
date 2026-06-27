import { afterEach, describe, expect, it, vi } from "vitest"
import { deleteFile, downloadUrl, listFiles, login, getSession } from "./admin-api"

function mockFetch(response: Partial<Response> & { json?: () => Promise<unknown> }) {
  const fn = vi.fn().mockResolvedValue({
    ok: true,
    status: 200,
    json: async () => ({}),
    ...response,
  })
  vi.stubGlobal("fetch", fn)
  return fn
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("admin-api", () => {
  it("getSession parses the response", async () => {
    mockFetch({ json: async () => ({ authenticated: true, username: "admin" }) })
    const session = await getSession()
    expect(session.authenticated).toBe(true)
    expect(session.username).toBe("admin")
  })

  it("login posts JSON credentials", async () => {
    const fetchFn = mockFetch({ json: async () => ({ authenticated: true }) })
    await login("admin", "secret")

    expect(fetchFn).toHaveBeenCalledWith(
      "/admin/api/login",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ username: "admin", password: "secret" }),
      }),
    )
  })

  it("listFiles builds query params and omits empties", async () => {
    const fetchFn = mockFetch({ json: async () => ({ items: [], total: 0, limit: 50, offset: 0 }) })
    await listFiles({ ip: "10.0.0.1", limit: 50, offset: 0, search: "" })

    const url = fetchFn.mock.calls[0][0] as string
    expect(url).toContain("ip=10.0.0.1")
    expect(url).toContain("limit=50")
    expect(url).not.toContain("search=")
  })

  it("deleteFile encodes the id and uses DELETE", async () => {
    const fetchFn = mockFetch({ status: 204 })
    await deleteFile("a/b")

    expect(fetchFn).toHaveBeenCalledWith(
      "/admin/api/files/a%2Fb",
      expect.objectContaining({ method: "DELETE" }),
    )
  })

  it("throws with the server error message on failure", async () => {
    mockFetch({ ok: false, status: 401, json: async () => ({ error: "invalid credentials" }) })
    await expect(login("admin", "wrong")).rejects.toThrow("invalid credentials")
  })

  it("downloadUrl points at the public download route", () => {
    expect(downloadUrl("abc")).toBe("/d/abc")
  })
})
