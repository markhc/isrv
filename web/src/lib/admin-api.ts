// Typed client for the admin API. All requests are same-origin so the signed
// session cookie set by the backend is sent automatically.

export interface AdminFile {
  id: string
  name: string
  size: number
  contentType: string
  expiration: string
  downloads: number
  encryptionVersion: number
  ipAddress?: string
  createdAt?: string
}

export interface ListFilesParams {
  search?: string
  ip?: string
  sortBy?: string
  sortDir?: "asc" | "desc"
  limit?: number
  offset?: number
}

export interface ListFilesResult {
  items: AdminFile[]
  total: number
  limit: number
  offset: number
}

export interface SessionState {
  authenticated: boolean
  username?: string
}

const BASE = "/admin/api"

async function parseError(res: Response): Promise<never> {
  let message = `request failed: ${res.status}`
  try {
    const body = (await res.json()) as { error?: string }
    if (body.error) message = body.error
  } catch {
    // response had no JSON body; keep the default message
  }
  throw new Error(message)
}

export async function getSession(): Promise<SessionState> {
  const res = await fetch(`${BASE}/session`, { credentials: "same-origin" })
  if (!res.ok) return parseError(res)
  return (await res.json()) as SessionState
}

export async function login(username: string, password: string): Promise<SessionState> {
  const res = await fetch(`${BASE}/login`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  })
  if (!res.ok) return parseError(res)
  return (await res.json()) as SessionState
}

export async function logout(): Promise<void> {
  const res = await fetch(`${BASE}/logout`, {
    method: "POST",
    credentials: "same-origin",
  })
  if (!res.ok) return parseError(res)
}

export async function listFiles(params: ListFilesParams): Promise<ListFilesResult> {
  const query = new URLSearchParams()
  if (params.search) query.set("search", params.search)
  if (params.ip) query.set("ip", params.ip)
  if (params.sortBy) query.set("sortBy", params.sortBy)
  if (params.sortDir) query.set("sortDir", params.sortDir)
  if (params.limit != null) query.set("limit", String(params.limit))
  if (params.offset != null) query.set("offset", String(params.offset))

  const res = await fetch(`${BASE}/files?${query.toString()}`, {
    credentials: "same-origin",
  })
  if (!res.ok) return parseError(res)
  return (await res.json()) as ListFilesResult
}

export async function deleteFile(id: string): Promise<void> {
  const res = await fetch(`${BASE}/files/${encodeURIComponent(id)}`, {
    method: "DELETE",
    credentials: "same-origin",
  })
  if (!res.ok) return parseError(res)
}

// downloadUrl returns the public URL used to fetch/preview a file's contents.
export function downloadUrl(id: string): string {
  return `/d/${encodeURIComponent(id)}`
}
