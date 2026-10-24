import client from '@/shared/api/client'
import { catalogueFromResponse, EMOJI_CATALOGUE_PATH, type CatalogueResult } from '../domain/emojiCatalogue'

/**
 * Conditional fetch of the custom emoji catalogue.
 *
 * A controlling service worker does the request when it understands the
 * message (it acks immediately). Dev, a page the worker does not control yet,
 * and an older worker fall through to a dedicated module worker. Tests call
 * the API client on the main thread. None of these block the picker: the
 * store paints localStorage before this promise settles.
 */
export function loadCustomEmojiCatalogue(revision: string | null): Promise<CatalogueResult> {
  if (import.meta.env.VITEST) return requestViaClient(revision)
  return loadInBackground(revision)
}

async function loadInBackground(revision: string | null): Promise<CatalogueResult> {
  if (navigator.serviceWorker?.controller) {
    try {
      return await askServiceWorker(revision)
    } catch (error) {
      if (!isUnavailable(error)) throw error
    }
  }
  try {
    const viaWorker = await askDedicatedWorker(revision)
    if (viaWorker) return viaWorker
  } catch (error) {
    if (!isUnavailable(error)) throw error
  }
  return requestViaClient(revision)
}

const ACK_MS = 250
const RESULT_MS = 20000

function askServiceWorker(revision: string | null): Promise<CatalogueResult> {
  const controller = navigator.serviceWorker.controller
  if (!controller) return Promise.reject(unavailable())
  return new Promise((resolve, reject) => {
    const channel = new MessageChannel()
    let accepted = false
    let timer = window.setTimeout(() => reject(unavailable()), ACK_MS)
    channel.port1.onmessage = (event: MessageEvent) => {
      const data = event.data as { accepted?: boolean, error?: boolean, status?: number, etag?: string | null, body?: unknown } | null
      if (data?.accepted) {
        accepted = true
        window.clearTimeout(timer)
        timer = window.setTimeout(() => reject(new Error('emoji catalogue failed')), RESULT_MS)
        return
      }
      window.clearTimeout(timer)
      if (!accepted || !data || data.error) {
        reject(new Error('emoji catalogue failed'))
        return
      }
      try {
        resolve(catalogueFromResponse(revision, data.status ?? 0, data.etag ?? null, data.body))
      } catch (error) {
        reject(error instanceof Error ? error : new Error('emoji catalogue failed'))
      }
    }
    try {
      controller.postMessage({ type: 'emoji-revalidate', revision }, [channel.port2])
    } catch {
      window.clearTimeout(timer)
      reject(unavailable())
    }
  })
}

interface WorkerReply { id: number, error?: boolean, status?: number, etag?: string | null, body?: unknown }

let worker: Worker | null = null
let nextJob = 0
const jobs = new Map<number, { resolve: (result: CatalogueResult) => void, reject: (error: Error) => void }>()

function dedicatedWorker(): Worker | null {
  if (typeof Worker === 'undefined') return null
  if (worker) return worker
  try {
    worker = new Worker(new URL('./emojiCatalogue.worker.ts', import.meta.url), { type: 'module' })
  } catch {
    return null
  }
  worker.onmessage = (event: MessageEvent<WorkerReply>) => {
    const job = jobs.get(event.data.id)
    if (!job) return
    jobs.delete(event.data.id)
    if (event.data.error) {
      job.reject(new Error('emoji catalogue failed'))
      return
    }
    try {
      job.resolve(catalogueFromResponse(null, event.data.status ?? 0, event.data.etag ?? null, event.data.body))
    } catch (error) {
      job.reject(error instanceof Error ? error : new Error('emoji catalogue failed'))
    }
  }
  worker.onerror = () => {
    for (const job of jobs.values()) job.reject(new Error('emoji catalogue failed'))
    jobs.clear()
    worker = null
  }
  return worker
}

function askDedicatedWorker(revision: string | null): Promise<CatalogueResult | null> {
  const current = dedicatedWorker()
  if (!current) return Promise.reject(unavailable())
  const id = ++nextJob
  return new Promise((resolve, reject) => {
    jobs.set(id, {
      resolve: (result) => resolve(revisionAware(revision, result)),
      reject,
    })
    current.postMessage({ id, revision })
  })
}

// The worker does not know which revision was sent. A 304 must keep it.
function revisionAware(sent: string | null, result: CatalogueResult): CatalogueResult {
  if (!result.unchanged) return result
  return { unchanged: true, revision: sent, emojis: [] }
}

async function requestViaClient(revision: string | null): Promise<CatalogueResult> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (revision) headers['If-None-Match'] = revision
  const response = await client.get<unknown>(EMOJI_CATALOGUE_PATH, {
    headers,
    validateStatus: (status: number) => status === 200 || status === 304,
  })
  return catalogueFromResponse(revision, response.status || 200, headerETag(response.headers), response.status === 304 ? null : response.data)
}

function headerETag(headers: unknown): string | null {
  if (!headers || typeof headers !== 'object') return null
  const bag = headers as { get?: (name: string) => unknown, etag?: unknown, ETag?: unknown }
  const fromGet = typeof bag.get === 'function' ? bag.get('etag') ?? bag.get('ETag') : undefined
  const value = fromGet ?? bag.etag ?? bag.ETag
  return typeof value === 'string' && value.trim() ? value : null
}

function unavailable(): Error {
  return Object.assign(new Error('emoji-worker-unavailable'), { code: 'unavailable' })
}

function isUnavailable(error: unknown): boolean {
  return error instanceof Error && (error as { code?: string }).code === 'unavailable'
}
