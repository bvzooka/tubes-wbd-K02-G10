export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public fields: Record<string, string> = {},
    public requestId = '',
  ) {
    super(message)
  }
}

let csrfToken = ''

export function setCsrfToken(token: string) {
  csrfToken = token
}

type Method = 'GET' | 'POST' | 'PUT' | 'DELETE'

export async function api<T = void>(method: Method, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET' && csrfToken) headers['X-CSRF-Token'] = csrfToken

  const res = await fetch('/api/v1' + path, {
    method,
    headers,
    credentials: 'same-origin',
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T

  const json = await res.json().catch(() => null)
  if (!res.ok) {
    const err = json?.error ?? {}
    if (res.status === 401 && !path.startsWith('/auth/')) redirectToLogin()
    throw new ApiError(res.status, err.code ?? 'unknown', err.message ?? res.statusText, err.fields, err.request_id)
  }
  return json.data as T
}

export function redirectToLogin() {
  const next = window.location.pathname + window.location.search
  window.location.assign('/login?next=' + encodeURIComponent(next))
}
