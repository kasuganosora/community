import client from '@/shared/api/client'
import type { MastodonStatus, MastodonCard, MastodonContext, MastodonPoll, MastodonAttachment, MastodonStatusSource, MastodonEmojiReaction } from '@/shared/types/mastodon'
import type { CreateStatusParams, ReplaceStatusParams } from '../domain/entities'

/** Factory for status action endpoints that return a single MastodonStatus */
function createStatusAction(action: string) {
  return async (id: string): Promise<MastodonStatus> => {
    const { data } = await client.post<MastodonStatus>(`/api/v1/statuses/${encodeURIComponent(id)}/${action}`)
    return data
  }
}

export const postApi = {
  async getCard(id: string, signal?: AbortSignal): Promise<MastodonCard | null> {
    const { data } = await client.get<MastodonCard | null>(`/api/v1/statuses/${encodeURIComponent(id)}/card`, { signal })
    return data
  },
  async react(id: string, name: string, remove = false): Promise<MastodonStatus> {
    const path = `/api/v1/statuses/${encodeURIComponent(id)}/reactions/${encodeURIComponent(name)}`
    const { data } = remove ? await client.delete<MastodonStatus>(path) : await client.put<MastodonStatus>(path)
    return data
  },
  async reactions(id: string): Promise<MastodonEmojiReaction[]> {
    const { data } = await client.get<MastodonEmojiReaction[]>(`/api/v1/statuses/${encodeURIComponent(id)}/reactions`)
    return data
  },
  async create(params: CreateStatusParams): Promise<MastodonStatus> {
    const { data } = await client.post<MastodonStatus>('/api/v1/statuses', params)
    return data
  },

  async getStatus(id: string): Promise<MastodonStatus> {
    const { data } = await client.get<MastodonStatus>(`/api/v1/statuses/${encodeURIComponent(id)}`)
    return data
  },

  async getContext(id: string): Promise<MastodonContext> {
    const { data } = await client.get<MastodonContext>(`/api/v1/statuses/${encodeURIComponent(id)}/context`)
    return data
  },

  reblog: createStatusAction('reblog'),
  unreblog: createStatusAction('unreblog'),
  favourite: createStatusAction('favourite'),
  unfavourite: createStatusAction('unfavourite'),
  bookmark: createStatusAction('bookmark'),
  unbookmark: createStatusAction('unbookmark'),
  pin: createStatusAction('pin'),
  unpin: createStatusAction('unpin'),

  async deleteStatus(id: string): Promise<void> {
    await client.delete(`/api/v1/statuses/${encodeURIComponent(id)}`)
  },

  async replaceStatus(id: string, params: ReplaceStatusParams): Promise<MastodonStatus> {
    const { data } = await client.post<MastodonStatus>(`/api/v1/statuses/${encodeURIComponent(id)}/replace`, params)
    return data
  },

  async getSource(id: string): Promise<MastodonStatusSource> {
    const { data } = await client.get<MastodonStatusSource>(`/api/v1/statuses/${encodeURIComponent(id)}/source`)
    return data
  },

  async votePoll(pollId: string, choices: number[]): Promise<MastodonPoll> {
    const { data } = await client.post<MastodonPoll>(`/api/v1/polls/${encodeURIComponent(pollId)}/votes`, { choices })
    return data
  },

  /**
   * Upload a media file. Images and audio use synchronous /api/v1/media.
   * Video uses /api/v2/media and polls /processing until the durable task
   * is ready, failed, or cancelled.
   */
  async uploadMedia(file: File, description?: string, options?: { signal?: AbortSignal; onProgress?: (percent: number) => void; onPhase?: (phase: UploadPhase, percent: number) => void }): Promise<MastodonAttachment> {
    const form = new FormData()
    form.append('file', file)
    if (description) form.append('description', description)
    const video = file.type.startsWith('video/') || /\.(mp4|webm|mov|m4v)$/i.test(file.name)
    const { data } = await client.post<MastodonAttachment>(video ? '/api/v2/media' : '/api/v1/media', form, {
      headers: { 'Content-Type': 'multipart/form-data' },
      timeout: video ? 60000 : 120000,
      signal: options?.signal,
      onUploadProgress: options?.onProgress ? event => {
        const total = event.total || file.size
        if (total > 0) options.onProgress!(Math.min(100, Math.round(event.loaded / total * 100)))
      } : undefined,
    })
    if (!video) return data
    rememberPendingMedia(data.id, file.name)
    try {
      return await pollMedia(data.id, options)
    } finally {
      forgetPendingMedia(data.id)
    }
  },

  resumeMedia(id: string, options?: { signal?: AbortSignal; onPhase?: (phase: UploadPhase, percent: number) => void }): Promise<MastodonAttachment> {
    return pollMedia(id, options)
  },
}

const pendingMediaKey = 'community.pendingMedia'

export function rememberPendingMedia(id: string, name: string) {
  const rows = loadPendingMedia().filter(row => row.id !== id)
  rows.push({ id, name })
  sessionStorage.setItem(pendingMediaKey, JSON.stringify(rows))
}

export function forgetPendingMedia(id: string) {
  const rows = loadPendingMedia().filter(row => row.id !== id)
  sessionStorage.setItem(pendingMediaKey, JSON.stringify(rows))
}

export function loadPendingMedia(): { id: string; name: string }[] {
  try {
    const parsed = JSON.parse(sessionStorage.getItem(pendingMediaKey) || '[]') as { id?: string; name?: string }[]
    return parsed.filter(row => row.id).map(row => ({ id: String(row.id), name: row.name || String(row.id) }))
  } catch {
    return []
  }
}

async function pollMedia(id: string, options?: { signal?: AbortSignal; onPhase?: (phase: UploadPhase, percent: number) => void }): Promise<MastodonAttachment> {
  options?.onPhase?.('queued', 0)
  const encoded = encodeURIComponent(id)
  try {
    for (;;) {
      if (options?.signal?.aborted) throw new DOMException('aborted', 'AbortError')
      const task = (await client.get<{ state: UploadPhase; progress?: number; error?: string }>(`/api/v1/media/${encoded}/processing`, { signal: options?.signal })).data
      options?.onPhase?.(task.state, task.progress ?? 0)
      if (task.state === 'ready') {
        forgetPendingMedia(id)
        return (await client.get<MastodonAttachment>(`/api/v1/media/${encoded}`, { signal: options?.signal })).data
      }
      if (task.state === 'failed' || task.state === 'cancelled') throw new Error(task.error || 'media processing failed')
      await waitForPoll(400, options?.signal)
    }
  } catch (err) {
    if (options?.signal?.aborted) {
      forgetPendingMedia(id)
      await client.delete(`/api/v1/media/${encoded}`).catch(() => undefined)
    }
    throw err
  }
}

export type UploadPhase = 'uploading' | 'queued' | 'processing' | 'saving' | 'ready' | 'failed' | 'cancelled'

function waitForPoll(ms: number, signal?: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal?.aborted) {
      reject(new DOMException('aborted', 'AbortError'))
      return
    }
    const timer = setTimeout(resolve, ms)
    signal?.addEventListener('abort', () => {
      clearTimeout(timer)
      reject(new DOMException('aborted', 'AbortError'))
    }, { once: true })
  })
}
