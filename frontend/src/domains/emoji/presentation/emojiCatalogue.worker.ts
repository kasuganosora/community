import { EMOJI_CATALOGUE_PATH } from '../domain/emojiCatalogue'

interface CatalogueJob { id: number, revision: string | null }
interface CatalogueReply { id: number, error?: boolean, status?: number, etag?: string | null, body?: unknown }

// Dedicated-worker fallback. Typed against the DOM lib on purpose: the app
// tsconfig includes every src file, and a webworker lib reference clashes
// with Window. The page interprets status, ETag and body.
const scope = self as unknown as {
  onmessage: ((event: MessageEvent<CatalogueJob>) => void) | null
  postMessage: (message: CatalogueReply) => void
}

scope.onmessage = (event) => {
  void fetchCatalogue(event.data.id, event.data.revision)
}

async function fetchCatalogue(id: number, revision: string | null) {
  const headers = new Headers({ Accept: 'application/json' })
  if (revision) headers.set('If-None-Match', revision)
  try {
    const res = await fetch(EMOJI_CATALOGUE_PATH, {
      method: 'GET',
      headers,
      credentials: 'same-origin',
      cache: 'no-store',
      signal: AbortSignal.timeout(20000),
    })
    const body = res.status === 304 || !res.ok ? null : await res.json() as unknown
    scope.postMessage({ id, status: res.status, etag: res.headers.get('ETag'), body })
  } catch {
    scope.postMessage({ id, error: true })
  }
}
