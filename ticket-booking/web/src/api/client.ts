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
}

/** Thin fetch wrapper: bearer token, JSON in/out, typed errors matching
 * the backend's {code,message} error shape (docs/plan.md API contract). */
export async function apiFetch<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (opts.token) headers['Authorization'] = `Bearer ${opts.token}`

  const res = await fetch(`${BASE_URL}${path}`, {
    method: opts.method ?? 'GET',
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  })

  if (res.status === 204) return undefined as T

  const contentType = res.headers.get('content-type') ?? ''
  const payload = contentType.includes('application/json') ? await res.json() : await res.arrayBuffer()

  if (!res.ok) {
    const body = payload as { code?: string; message?: string }
    throw new ApiError(res.status, body.code ?? 'unknown', body.message ?? res.statusText, body)
  }
  return payload as T
}

export function apiBinaryUrl(path: string): string {
  return `${BASE_URL}${path}`
}

export { BASE_URL }
