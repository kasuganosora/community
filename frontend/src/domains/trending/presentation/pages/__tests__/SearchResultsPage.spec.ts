import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/shared/api/client', () => ({ default: { get } }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: { q: 'godot' } }), useRouter: () => ({ replace: vi.fn(() => Promise.resolve()), push: vi.fn() }) }))
vi.mock('@/domains/auth/presentation/stores/authStore', () => ({ useAuthStore: () => ({ isAuthenticated: true, currentUser: { id: 'me' } }) }))
vi.mock('@/domains/user/presentation/composables/useRelationships', () => ({ useRelationships: () => ({ load: vi.fn() }) }))
vi.mock('@/domains/post/presentation/composables/useStatusActions', () => ({ useStatusActions: () => ({}), applyStatusUpdate: vi.fn() }))
import SearchResultsPage from '../SearchResultsPage.vue'

const post = (id: string) => ({ id })
beforeEach(() => get.mockReset())

it('asks for a small local first page and loads more by type+offset', async () => {
  get.mockResolvedValueOnce({ data: { statuses: Array.from({ length: 10 }, (_, i) => post(String(i))), accounts: [], hashtags: [] } })
  const w = mount(SearchResultsPage, { global: { stubs: { PostCard: true, AccountRow: true, TabBar: true, SearchBox: true, GroupSearchResults: true } } })
  await flushPromises()
  expect(get.mock.calls[0][1].params).toMatchObject({ q: 'godot', limit: 10, resolve: false })
  get.mockResolvedValueOnce({ data: { statuses: [post('9'), post('10')] } })
  await w.get('.search-more button').trigger('click')
  await flushPromises()
  expect(get.mock.calls[1][1].params).toMatchObject({ q: 'godot', type: 'statuses', offset: 10, limit: 10, resolve: false })
  expect(w.findAll('post-card-stub')).toHaveLength(11)
  expect(w.find('.search-more').exists()).toBe(false)
})
