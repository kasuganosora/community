import { beforeEach, describe, expect, it, vi } from 'vitest'
import client from '@/shared/api/client'
import { postApi } from '../postApi'

vi.mock('@/shared/api/client', () => ({ default: { post: vi.fn(), get: vi.fn(), delete: vi.fn() } }))

const post = vi.mocked(client.post)
const get = vi.mocked(client.get)
const del = vi.mocked(client.delete)

beforeEach(() => {
  post.mockReset()
  get.mockReset()
  del.mockReset()
  sessionStorage.clear()
})

describe('uploadMedia', () => {
  it('posts an image only to the synchronous endpoint', async () => {
    post.mockResolvedValue({ data: { id: 'img', type: 'image', url: '/a.webp' } })
    const file = new File(['png'], 'photo.png', { type: 'image/png' })
    const result = await postApi.uploadMedia(file)
    expect(post).toHaveBeenCalledOnce()
    expect(post.mock.calls[0]?.[0]).toBe('/api/v1/media')
    expect(get).not.toHaveBeenCalled()
    expect(result.id).toBe('img')
  })

  it('polls video processing until ready and reports phases', async () => {
    post.mockResolvedValue({ data: { id: 'vid', type: 'unknown', url: '' } })
    const phases: string[] = []
    get.mockImplementation((url: string) => {
      if (String(url).includes('/processing')) {
        const step = phases.length
        const body = step === 0
          ? { state: 'queued', progress: 0 }
          : step === 1
            ? { state: 'processing', progress: 40 }
            : { state: 'ready', progress: 100 }
        return Promise.resolve({ data: body })
      }
      return Promise.resolve({ data: { id: 'vid', type: 'video', url: '/clip.webm' } })
    })
    const file = new File(['mp4'], 'clip.mp4', { type: 'video/mp4' })
    const result = await postApi.uploadMedia(file, undefined, { onPhase: phase => { phases.push(phase) } })
    expect(post.mock.calls[0]?.[0]).toBe('/api/v2/media')
    expect(phases).toEqual(['queued', 'processing', 'ready'])
    expect(result).toMatchObject({ id: 'vid', url: '/clip.webm' })
  })

  it('reports saving and surfaces a processing failure', async () => {
    post.mockResolvedValue({ data: { id: 'vid', type: 'unknown', url: '' } })
    const phases: string[] = []
    get.mockImplementation((url: string) => {
      if (String(url).includes('/processing')) {
        return phases.includes('saving')
          ? Promise.resolve({ data: { state: 'failed', progress: 100, error: 'media processing failed' } })
          : Promise.resolve({ data: { state: 'saving', progress: 100 } })
      }
      return Promise.resolve({ data: { id: 'vid', type: 'video', url: '/clip.webm' } })
    })
    const file = new File(['mp4'], 'clip.mp4', { type: 'video/mp4' })
    await expect(postApi.uploadMedia(file, undefined, { onPhase: phase => { phases.push(phase) } })).rejects.toThrow('media processing failed')
    expect(phases).toContain('saving')
    expect(phases).toContain('failed')
  })

  it('deletes the attachment when the poll is aborted', async () => {
    post.mockResolvedValue({ data: { id: 'vid', type: 'unknown', url: '' } })
    const controller = new AbortController()
    get.mockImplementation(() => {
      controller.abort()
      return Promise.resolve({ data: { state: 'processing', progress: 10 } })
    })
    del.mockResolvedValue({ data: {} })
    const file = new File(['mp4'], 'clip.webm', { type: 'video/webm' })
    await expect(postApi.uploadMedia(file, undefined, { signal: controller.signal, onPhase: () => {} })).rejects.toThrow()
    expect(del).toHaveBeenCalledWith('/api/v1/media/vid')
    expect(sessionStorage.getItem('community.pendingMedia')).toBeNull()
  })

  it('keeps the task when observation stops and deletes it on an explicit cancel', async () => {
    post.mockResolvedValue({ data: { id: 'vid', type: 'unknown', url: '' } })
    const controller = new AbortController()
    get.mockImplementation(() => {
      controller.abort(new DOMException('dispose', 'AbortError'))
      return Promise.resolve({ data: { state: 'processing', progress: 10 } })
    })
    const file = new File(['mp4'], 'clip.mp4', { type: 'video/mp4' })
    await expect(postApi.uploadMedia(file, undefined, { signal: controller.signal, ownerId: 'user-a', onPhase: () => {} })).rejects.toThrow()
    expect(del).not.toHaveBeenCalled()
    expect(JSON.parse(sessionStorage.getItem('community.pendingMedia') || '[]')).toEqual([
      expect.objectContaining({ id: 'vid', owner: 'user-a' }),
    ])
    del.mockResolvedValue({ data: {} })
    get.mockResolvedValue({ data: { state: 'processing', progress: 20 } })
    const cancel = new AbortController()
    const pending = postApi.resumeMedia('vid', { signal: cancel.signal })
    cancel.abort(new DOMException('cancel', 'AbortError'))
    await expect(pending).rejects.toThrow()
    expect(del).toHaveBeenCalledWith('/api/v1/media/vid')
    expect(JSON.parse(sessionStorage.getItem('community.pendingMedia') || '[]')).toEqual([])
  })
})
