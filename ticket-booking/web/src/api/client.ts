const BASE_URL = (import.meta.env.VITE_API_URL as string | undefined) ?? 'http://localhost:8080'

export class ApiError extends Error {
  status: number
  code: string
  body?: unknown

  constructor(status: number, code: string, message: string, body?: unknown) {
    super(message)
    this.status = status
    this.code = code
    this.body = body
  }
}

interface RequestOptions {
  method?: string
  body?: unknown
  token?: string | null
  /** Extra headers beyond Authorization/Content-Type — e.g. the waiting
   * room's X-Admission-Token on POST .../holds. */
  headers?: Record<string, string>
}

/** Thin fetch wrapper: bearer token, JSON in/out, typed errors matching
 * the backend's {code,message} error shape (docs/plan.md API contract). */
export async function apiFetch<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json', ...opts.headers }
  if (opts.token) headers['Authorization'] = `Bearer ${opts.token}`

  const res = await fetch(`${BASE_URL}${path}`, {
    method: opts.method ?? 'GET',
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  })

  if (res.status === 204) return undefined as T

  const contentType = res.headers.get('content-type') ?? ''
  const isJson = contentType.includes('application/json')
  const payload = isJson ? await res.json() : await res.arrayBuffer()

  if (!res.ok) {
    // Some handlers (e.g. RequireAdmission's http.Error calls) send a JSON
    // body mislabeled as text/plain — try to parse it anyway before
    // falling back to a bare status text, so `code` still comes through.
    let body: { code?: string; message?: string } = {}
    if (isJson) {
      body = payload as { code?: string; message?: string }
    } else {
      try {
        body = JSON.parse(new TextDecoder().decode(payload as ArrayBuffer))
      } catch {
        // not JSON either — body stays {}, message falls back to statusText below
      }
    }
    throw new ApiError(res.status, body.code ?? 'unknown', body.message ?? res.statusText, body)
  }
  return payload as T
}

export function apiBinaryUrl(path: string): string {
  return `${BASE_URL}${path}`
}

export { BASE_URL }
