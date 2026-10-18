export interface SetupError { error_code: string; detail?: string }

export class SetupApiError extends Error {
  code: string
  detail: string
  status: number
  constructor(code: string, detail = '', status = 0) {
    super(code)
    this.code = code
    this.detail = detail
    this.status = status
  }
}

const TOKEN_KEY = 'setup-token'

export function getToken(): string {
  try { return sessionStorage.getItem(TOKEN_KEY) || '' } catch { return '' }
}
export function setToken(token: string): void {
  try { sessionStorage.setItem(TOKEN_KEY, token) } catch { /* storage unavailable */ }
}

export async function call<T = Record<string, unknown>>(method: 'GET' | 'POST', path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  let res: Response
  try {
    res = await fetch(`/api/setup/${path}`, {
      method,
      headers: { 'Content-Type': 'application/json', 'X-Setup-Token': getToken() },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    })
  } catch {
    throw new SetupApiError('network')
  }
  let data: unknown = null
  try { data = await res.json() } catch { /* empty body */ }
  if (!res.ok) {
    const e = (data || {}) as Partial<SetupError>
    throw new SetupApiError(e.error_code || (res.status === 404 ? 'not_found' : 'unknown'), e.detail || '', res.status)
  }
  return data as T
}

export interface StageProgress { phase: string; done: number; total: number; rate: number; eta_seconds: number; updated_at: string }
export interface Stage { name: string; status: string; counts?: Record<string, number>; error?: SetupError; progress?: StageProgress }
export interface Job {
  id: string; mode: string; status: string; locale: string; base_url: string; stages: Stage[]; log: { time: string; level: string; message: string }[]
  heartbeat_at?: string; server_time?: string; updated_at?: string
}
export interface Inspection {
  counts: Record<string, number>
  id_method?: string
  detected_base_url: string
  base_url_candidates?: string[]
  uses_object_storage?: boolean
  object_storage?: { endpoint: string; bucket: string; region: string; access_key: string; public_url: string; path_style: boolean }
  has_object_secret?: boolean
}
