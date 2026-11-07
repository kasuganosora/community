import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import PostComposer from '../PostComposer.vue'
import client from '@/shared/api/client'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { pendingMediaTTL } from '@/domains/post/infrastructure/postApi'
import { invalidateSessionData } from '@/shared/api/sessionScope'
import type { MastodonAccount } from '@/shared/types/mastodon'

vi.mock('@/shared/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/shared/api/client')>()
  return { ...actual, default: { post: vi.fn(), get: vi.fn(), delete: vi.fn() } }
})

const post = vi.mocked(client.post)
const get = vi.mocked(client.get)
const del = vi.mocked(client.delete)

function mountAs(id: string) {
  const pinia = createPinia()
  const auth = useAuthStore(pinia)
  auth.currentUser = { id, acct: id } as MastodonAccount
  const wrapper = shallowMount(PostComposer, { global: { plugins: [pinia], renderStubDefaultSlot: true } })
  return { pinia, wrapper }
}

async function startVideo(wrapper: ReturnType<typeof shallowMount>) {
  const input = wrapper.get('input[type="file"]')
  Object.defineProperty(input.element, 'files', { configurable: true, value: [new File(['video'], 'clip.mp4', { type: 'video/mp4' })] })
  await input.trigger('change')
  await wrapper.trigger('keydown', { key: 'Enter', ctrlKey: true })
  await flushPromises()
}

afterEach(() => {
  sessionStorage.clear()
  vi.clearAllMocks()
})

describe('PostComposer refresh keeps an accepted task', () => {
  it('unmounts without deleting and resumes the same task without posting', async () => {
    let resumed = false
    post.mockImplementation((url: string) => {
      if (url === '/api/v2/media') return Promise.resolve({ data: { id: 'task-1', type: 'unknown', url: '' } })
      return Promise.resolve({ data: { id: 'should-not-post' } })
    })
    get.mockImplementation((url: string) => {
      if (String(url).includes('/processing')) {
        return Promise.resolve({ data: resumed ? { state: 'ready', progress: 100 } : { state: 'processing', progress: 15 } })
      }
      return Promise.resolve({ data: { id: 'task-1', type: 'video', url: '/clip.webm' } })
    })
    const { pinia, wrapper } = mountAs('user-a')
    await startVideo(wrapper)
    expect(post.mock.calls.map(call => call[0])).toContain('/api/v2/media')
    expect(get).toHaveBeenCalled()
    const polls = get.mock.calls.length
    wrapper.unmount()
    await flushPromises()
    expect(del).not.toHaveBeenCalled()
    expect(JSON.parse(sessionStorage.getItem('community.pendingMedia') || '[]')).toEqual([
      expect.objectContaining({ id: 'task-1', owner: 'user-a', name: 'clip.mp4' }),
    ])

    resumed = true
    const again = shallowMount(PostComposer, { global: { plugins: [pinia], renderStubDefaultSlot: true } })
    await flushPromises()
    expect(del).not.toHaveBeenCalled()
    expect(get.mock.calls.length).toBeGreaterThan(polls)
    expect(get.mock.calls.some(call => String(call[0]) === '/api/v1/media/task-1/processing')).toBe(true)
    expect(get.mock.calls.some(call => String(call[0]) === '/api/v1/media/task-1')).toBe(true)
    expect(again.text()).toContain('已上传')
    expect(again.text()).toContain('clip.mp4')
    expect(post.mock.calls.map(call => call[0])).not.toContain('/api/v1/statuses')
    expect(again.text()).not.toContain('should-not-post')
    again.unmount()
  })

  it('deletes the task when the user removes it', async () => {
    post.mockResolvedValue({ data: { id: 'task-1', type: 'unknown', url: '' } })
    get.mockResolvedValue({ data: { state: 'processing', progress: 15 } })
    del.mockResolvedValue({ data: {} })
    const { wrapper } = mountAs('user-a')
    await startVideo(wrapper)
    expect(sessionStorage.getItem('community.pendingMedia')).toContain('task-1')
    await wrapper.get('.composer__media-remove').trigger('click')
    await flushPromises()
    expect(del).toHaveBeenCalledWith('/api/v1/media/task-1')
    expect(JSON.parse(sessionStorage.getItem('community.pendingMedia') || '[]')).toEqual([])
    expect(post.mock.calls.map(call => call[0])).not.toContain('/api/v1/statuses')
    wrapper.unmount()
  })

  it('does not resume another user or an expired task', async () => {
    sessionStorage.setItem('community.pendingMedia', JSON.stringify([
      { id: 'theirs', name: 'other.mp4', owner: 'user-b', at: Date.now() },
      { id: 'stale', name: 'old.mp4', owner: 'user-a', at: Date.now() - pendingMediaTTL - 1000 },
    ]))
    const { wrapper } = mountAs('user-a')
    await flushPromises()
    expect(get.mock.calls.some(call => String(call[0]).includes('/processing'))).toBe(false)
    expect(wrapper.find('.composer__media-item').exists()).toBe(false)
    const left = JSON.parse(sessionStorage.getItem('community.pendingMedia') || '[]') as { id: string }[]
    expect(left.map(row => row.id)).not.toContain('theirs')
    expect(left.map(row => row.id)).not.toContain('stale')
    wrapper.unmount()
  })

  it('clears persisted tasks when the session changes', async () => {
    post.mockResolvedValue({ data: { id: 'task-1', type: 'unknown', url: '' } })
    get.mockResolvedValue({ data: { state: 'processing', progress: 15 } })
    del.mockResolvedValue({ data: {} })
    sessionStorage.setItem('community.pendingMedia', JSON.stringify([
      { id: 'theirs', name: 'other.mp4', owner: 'user-b', at: Date.now() },
    ]))
    const { wrapper } = mountAs('user-a')
    await flushPromises()
    await startVideo(wrapper)
    invalidateSessionData()
    await flushPromises()
    expect(del).toHaveBeenCalledWith('/api/v1/media/task-1')
    expect(sessionStorage.getItem('community.pendingMedia')).toBeNull()
    expect(post.mock.calls.map(call => call[0])).not.toContain('/api/v1/statuses')
    wrapper.unmount()
  })
})
