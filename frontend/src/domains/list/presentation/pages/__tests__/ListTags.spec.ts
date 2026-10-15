import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import ListTimelinePage from '../ListTimelinePage.vue'
import { listApi } from '../../../infrastructure/listApi'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { normalizeTagInput } from '../../../domain/list'
import type { MastodonAccount, MastodonList, MastodonTag } from '@/shared/types/mastodon'

const route = reactive({ params: { id: 'a' }, query: {} })
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/shared/composables/useFeedStream', () => ({ useFeedStream: () => {} }))
vi.mock('@/domains/post/presentation/composables/useStatusActions', () => ({ useStatusActions: () => ({}), applyStatusUpdate: vi.fn(), editedStatuses: new Map() }))
vi.mock('@/shared/ui/feedback', () => ({ MessagePlugin: { success: vi.fn() } }))
const global = { stubs: {
  TimelineFeed: true, AccountRow: { props: ['account'], template: '<div>{{ account.id }}<slot name="action" /></div>' },
  't-dialog': { props: ['visible'], template: '<div v-if="visible" data-dialog><slot /></div>' },
  't-loading': true, 't-icon': true, 'router-link': true,
} }
const tag = (name: string) => ({ name, url: '' }) as MastodonTag

beforeEach(() => {
  vi.restoreAllMocks(); setActivePinia(createPinia())
  useAuthStore().currentUser = { id: 'alice' } as MastodonAccount
  vi.spyOn(listApi, 'getList').mockResolvedValue({ id: 'a', title: 'A', replies_policy: 'list' } as MastodonList)
  vi.spyOn(listApi, 'getTimeline').mockResolvedValue([])
  vi.spyOn(listApi, 'getAccounts').mockResolvedValue([])
  vi.spyOn(userApi, 'getAllFollowing').mockResolvedValue([])
  vi.spyOn(listApi, 'getTags').mockResolvedValue([tag('godot')])
})
afterEach(() => vi.useRealTimers())

async function open() {
  const w = mount(ListTimelinePage, { global }); await flushPromises()
  await w.findAll('button').find(b => b.text() === '管理')!.trigger('click'); await flushPromises()
  return w
}

it('normalizes hashtag input', () => {
  expect(normalizeTagInput(' #Godot ')).toBe('Godot')
  expect(normalizeTagInput('游戏开发')).toBe('游戏开发')
  expect(normalizeTagInput('two words')).toBe('')
  expect(normalizeTagInput('#')).toBe('')
})

it('shows, adds (with autocomplete) and removes list hashtags', async () => {
  vi.useFakeTimers()
  const search = vi.spyOn(listApi, 'searchTags').mockResolvedValue([tag('godot'), tag('gamedev')])
  const add = vi.spyOn(listApi, 'addTags').mockResolvedValue([tag('godot'), tag('gamedev')])
  const remove = vi.spyOn(listApi, 'removeTags').mockResolvedValue([tag('gamedev')])
  const w = await open()
  expect(w.text()).toContain('话题（1/20）')
  expect(w.text()).toContain('#godot')

  await w.get('#list-tag-input').setValue('#gam')
  await vi.advanceTimersByTimeAsync(300); await flushPromises()
  expect(search).toHaveBeenCalledWith('gam', expect.anything())
  const suggestions = w.findAll('.tag-suggestions button').map(b => b.text())
  expect(suggestions).toEqual(['#gamedev']) // already-added tags are hidden
  await w.get('.tag-suggestions button').trigger('click'); await flushPromises()
  expect(add).toHaveBeenCalledWith('a', ['gamedev'])
  expect(w.text()).toContain('话题（2/20）')
  expect((w.get('#list-tag-input').element as HTMLInputElement).value).toBe('')

  await w.get('[aria-label="移除话题 godot"]').trigger('click'); await flushPromises()
  expect(remove).toHaveBeenCalledWith('a', ['godot'])
  expect(w.findAll('.tag-chip').map(c => c.text())).toEqual(['#gamedev'])
})

it('submits typed tags with Enter and shows server errors', async () => {
  vi.spyOn(listApi, 'searchTags').mockResolvedValue([])
  vi.spyOn(listApi, 'addTags').mockRejectedValue(new Error('a list can follow at most 20 hashtags'))
  const w = await open()
  await w.get('#list-tag-input').setValue('rust')
  await w.get('form.tag-add').trigger('submit'); await flushPromises()
  expect(listApi.addTags).toHaveBeenCalledWith('a', ['rust'])
  expect(w.get('[role="alert"]').text()).toContain('20')
})
