// Mock backend for integration tests. MSW intercepts fetch at the network
// layer so components exercise their real data-fetching code paths.
import { http, HttpResponse } from "msw"
import { setupServer } from "msw/node"
import type { AdminFile } from "@/lib/admin-api"

export const ADMIN_USERNAME = "admin"
export const ADMIN_PASSWORD = "secret"

interface AdminApiState {
  authenticated: boolean
  files: AdminFile[]
  // Contents served by GET /d/:id for text previews.
  fileContents: Record<string, string>
}

export function defaultFiles(): AdminFile[] {
  return [
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
}

// seedFiles generates n deterministic files, newest first by createdAt.
export function seedFiles(n: number): AdminFile[] {
  return Array.from({ length: n }, (_, i) => ({
    id: `seed-${i + 1}`,
    name: `file-${String(i + 1).padStart(3, "0")}.bin`,
    size: (i + 1) * 1024,
    contentType: "application/octet-stream",
    expiration: "2026-08-15T12:00:00Z",
    downloads: i,
    encryptionVersion: 0,
    ipAddress: `10.0.1.${(i % 254) + 1}`,
    createdAt: new Date(Date.UTC(2026, 5, 30) - i * 86_400_000).toISOString(),
  }))
}

function createDefaultState(): AdminApiState {
  return {
    authenticated: false,
    files: defaultFiles(),
    fileContents: { f1: "hello from readme" },
  }
}

export const adminApiState: AdminApiState = createDefaultState()

export function resetAdminApiState(): void {
  Object.assign(adminApiState, createDefaultState())
}

const SORT_ACCESSORS: Record<string, (f: AdminFile) => string | number> = {
  name: (f) => f.name,
  size: (f) => f.size,
  created_at: (f) => f.createdAt ?? "",
}

export const handlers = [
  // Anonymous upload endpoint (POST / shares the path with the SPA root).
  // The multipart body is inspected as text: request.formData() cannot be
  // used because undici rejects jsdom's cross-realm File instances, and the
  // original filename is not preserved through serialization either. The
  // handler therefore validates the multipart shape and returns fixed values.
  http.post("/", async ({ request }) => {
    const body = await request.text()
    if (!/name="file"/.test(body)) {
      return HttpResponse.text("no file provided", { status: 400 })
    }
    return HttpResponse.json({
      status: "created",
      filename: "uploaded-file",
      shortUrl: "http://files.example/d/abc123",
      expiration: "2026-08-01T12:00:00Z",
    })
  }),

  http.get("/admin/api/session", () => {
    if (!adminApiState.authenticated) {
      return HttpResponse.json({ authenticated: false })
    }
    return HttpResponse.json({ authenticated: true, username: ADMIN_USERNAME })
  }),

  http.post("/admin/api/login", async ({ request }) => {
    const body = (await request.json()) as { username?: string, password?: string }
    if (body.username === ADMIN_USERNAME && body.password === ADMIN_PASSWORD) {
      adminApiState.authenticated = true
      return HttpResponse.json({ authenticated: true, username: ADMIN_USERNAME })
    }
    return HttpResponse.json({ error: "invalid credentials" }, { status: 401 })
  }),

  http.post("/admin/api/logout", () => {
    adminApiState.authenticated = false
    return new HttpResponse(null, { status: 204 })
  }),

  http.get("/admin/api/files", ({ request }) => {
    if (!adminApiState.authenticated) {
      return HttpResponse.json({ error: "unauthorized" }, { status: 401 })
    }

    const url = new URL(request.url)
    const search = (url.searchParams.get("search") ?? "").toLowerCase()
    const ip = url.searchParams.get("ip") ?? ""
    const sortBy = url.searchParams.get("sortBy") ?? "created_at"
    const sortDir = url.searchParams.get("sortDir") ?? "desc"
    const limit = Number(url.searchParams.get("limit") ?? 25)
    const offset = Number(url.searchParams.get("offset") ?? 0)

    let items = adminApiState.files.filter((f) => {
      if (search && !f.name.toLowerCase().includes(search) && !f.contentType.toLowerCase().includes(search)) {
        return false
      }
      if (ip && f.ipAddress !== ip) return false
      return true
    })

    const accessor = SORT_ACCESSORS[sortBy]
    if (accessor) {
      items = [...items].sort((a, b) => {
        const av = accessor(a)
        const bv = accessor(b)
        const cmp = av < bv ? -1 : av > bv ? 1 : 0
        return sortDir === "desc" ? -cmp : cmp
      })
    }

    const total = items.length
    return HttpResponse.json({
      items: items.slice(offset, offset + limit),
      total,
      limit,
      offset,
    })
  }),

  http.delete("/admin/api/files/:id", ({ params }) => {
    if (!adminApiState.authenticated) {
      return HttpResponse.json({ error: "unauthorized" }, { status: 401 })
    }
    const id = params.id as string
    const index = adminApiState.files.findIndex((f) => f.id === id)
    if (index === -1) {
      return HttpResponse.json({ error: "not found" }, { status: 404 })
    }
    adminApiState.files.splice(index, 1)
    return new HttpResponse(null, { status: 204 })
  }),

  // Public download route, used by the admin detail panel for text previews.
  http.get("/d/:id", ({ params }) => {
    const content = adminApiState.fileContents[params.id as string]
    if (content == null) {
      return HttpResponse.text("not found", { status: 404 })
    }
    return HttpResponse.text(content)
  }),
]

export const server = setupServer(...handlers)
