import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, reactive } from 'vue'
import type { Group } from '@/domains/group/domain/group'
import { groupApi } from '@/domains/group/infrastructure/groupApi'
import { sessionEpoch } from '@/shared/api/sessionScope'
import GroupSearchResults from '../GroupSearchResults.vue'

const auth = reactive({ isAuthenticated: true })
vi.mock('@/domains/auth/presentation/stores/authStore', () => ({ useAuthStore: () => auth }))
vi.mock('@/domains/group/infrastructure/groupApi', () => ({ groupApi: { search: vi.fn(), resolve: vi.fn(), join: vi.fn(), joinStatus: vi.fn() } }))
const linkStub = defineComponent({ props: { to: { type: [String, Object], required: true } }, template: '<a :data-to="JSON.stringify(to)"><slot /></a>' })
const group = (id: string, extra: Partial<Group> = {}): Group => ({
  id, name: `群组 ${id}`, topic: '一起交流', membership: 'none', encryption: 'unknown', join_rule: 'public', listed: true,
  member_count: 4, unread_count: 0, client_url: `https://matrix.to/#/!${id}:example.test`,
  capabilities: { send_message: false, invite: false, kick: false, ban: false, redact: false, set_name: false, set_avatar: false, manage_permissions: false }, ...extra,
})
const setup = (query = '摄影') => mount(GroupSearchResults, {
  props: { query }, global: { stubs: { RouterLink: linkStub }, mocks: { $route: { fullPath: '/search?type=groups' } } },
})
beforeEach(() => {
  vi.clearAllMocks()
  auth.isAuthenticated = true
  sessionEpoch.value++
  vi.mocked(groupApi.search).mockResolvedValue({ groups: [], next_cursor: null, source: 'example.test' })
})

describe('Matrix group discovery', () => {
  it('keeps personal/public scopes separate and deduplicates the same room without exposing private results elsewhere', async () => {
    vi.mocked(groupApi.search).mockImplementation(async ({ scope }) => ({
      groups: scope === 'joined' ? [group('one', { membership: 'join' })] : [group('one'), group('two')], next_cursor: null, source: scope,
    }))
    const w = setup()
    await flushPromises()
    expect(groupApi.search).toHaveBeenCalledTimes(2)
    expect(w.findAll('article')).toHaveLength(2)
    expect(w.get('section[aria-label="我的群组"]').text()).toContain('群组 one')
    expect(w.get('section[aria-label="本站公开群"]').text()).not.toContain('群组 one')
    expect(groupApi.join).not.toHaveBeenCalled()
    w.unmount()
  })

  it('does not send keywords to a remote directory until its server is explicitly submitted', async () => {
    const w = setup()
    await flushPromises()
    await w.get('select').setValue('remote')
    await flushPromises()
    expect(groupApi.search).toHaveBeenCalledTimes(2)
    await w.get('#group-search-server').setValue('https://matrix.org/')
    await w.get('form').trigger('submit')
    expect(w.get('[role="alert"]').text()).toContain('不要填写网页链接')
    expect(groupApi.search).toHaveBeenCalledTimes(2)
    await w.get('#group-search-server').setValue('matrix.org')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(groupApi.search).toHaveBeenLastCalledWith(expect.objectContaining({ q: '摄影', scope: 'remote', server: 'matrix.org' }), expect.any(AbortSignal))
    expect(w.text()).toContain('不是全网搜索')
    await w.get('#group-search-server').setValue('another.example')
    await w.setProps({ query: '新的关键词' })
    await flushPromises()
    expect(groupApi.search).toHaveBeenCalledTimes(3)
    expect(w.text()).toContain('填写服务器名称后点击')
    w.unmount()
  })

  it('distinguishes directory failure from empty results and lets each failed source retry', async () => {
    vi.mocked(groupApi.search).mockImplementation(async ({ scope }) => {
      if (scope === 'local') throw new Error('目录查询超时，请稍后重试')
      return { groups: [group('mine', { membership: 'join' })], next_cursor: null, source: 'mine' }
    })
    const w = setup()
    await flushPromises()
    const local = w.get('section[aria-label="本站公开群"]')
    expect(local.get('[role="alert"]').text()).toContain('目录查询超时')
    expect(local.text()).not.toContain('没有找到')
    expect(w.text()).toContain('群组 mine')
    await local.get('button').trigger('click')
    await flushPromises()
    expect(groupApi.search).toHaveBeenCalledTimes(3)
    w.unmount()
  })

  it('paginates only the selected source and retries failed pagination without losing earlier results', async () => {
    let localCalls = 0
    vi.mocked(groupApi.search).mockImplementation(async ({ scope, cursor }) => {
      if (scope === 'joined') return { groups: [], next_cursor: null, source: scope }
      localCalls++
      if (localCalls === 2) throw new Error('临时不可用')
      return { groups: cursor ? [group('one'), group('two')] : [group('one')], next_cursor: cursor ? null : 'local-page-2', source: scope }
    })
    const w = setup()
    await flushPromises()
    await w.get('.group-search__more').trigger('click')
    await flushPromises()
    expect(w.findAll('article')).toHaveLength(1)
    await w.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(groupApi.search).toHaveBeenLastCalledWith(expect.objectContaining({ cursor: 'local-page-2', scope: 'local' }), expect.any(AbortSignal))
    expect(w.findAll('article')).toHaveLength(2)
    expect(w.find('.group-search__more').exists()).toBe(false)
    w.unmount()
  })

  it('discards stale results after a query or authenticated identity changes', async () => {
    let finish!: (value: Awaited<ReturnType<typeof groupApi.search>>) => void
    vi.mocked(groupApi.search).mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    const w = setup('旧搜索')
    await w.setProps({ query: '新搜索' })
    sessionEpoch.value++
    await flushPromises()
    finish({ groups: [group('private-old-user')], next_cursor: null, source: 'mine' })
    await flushPromises()
    expect(w.text()).not.toContain('private-old-user')
    w.unmount()
  })

  it('resolves exact references read-only and preserves via hints without claiming known encryption', async () => {
    vi.mocked(groupApi.resolve).mockResolvedValue({ group: group('opaque-v12'), target: '!opaque-v12', via: ['matrix.org'] })
    const w = setup('https://matrix.to/#/%23photography:matrix.org?via=matrix.org')
    await flushPromises()
    expect(groupApi.search).not.toHaveBeenCalled()
    expect(groupApi.resolve).toHaveBeenCalledTimes(1)
    expect(groupApi.join).not.toHaveBeenCalled()
    expect(w.text()).toContain('加密状态待确认')
    expect(w.text()).not.toContain('非端到端加密')
    expect(w.get('button').text()).toBe('加入群组')
    w.unmount()
  })

  it('handles a resolved address with no public metadata without joining', async () => {
    vi.mocked(groupApi.resolve).mockResolvedValue({ target: '!private:test.example', via: ['test.example'] })
    const w = setup('#private:test.example')
    await flushPromises()
    expect(w.text()).toContain('若群组已加密')
    expect(w.findAll('article')).toHaveLength(1)
    expect(groupApi.join).not.toHaveBeenCalled()
    w.unmount()
  })

  it('retains explicit route hints and signed previews while resolving opaque room addresses', async () => {
    vi.mocked(groupApi.resolve).mockResolvedValue({ target: '!opaque-v12', via: ['directory.example'], client_url: 'https://matrix.to/#/!opaque-v12?via=directory.example' })
    const w = setup('!opaque-v12')
    await w.setProps({ via: ['directory.example'], previewRef: 'signed-preview' })
    await flushPromises()
    expect(groupApi.resolve).toHaveBeenLastCalledWith('!opaque-v12', expect.any(AbortSignal), ['directory.example'], 'signed-preview')
    expect(groupApi.join).not.toHaveBeenCalled()
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joined', group: group('opaque-v12', { membership: 'join', encryption: 'unencrypted' }) })
    await w.get('.group-result__actions button').trigger('click'); await flushPromises()
    expect(groupApi.join).toHaveBeenCalledWith({ target: '!opaque-v12', via: ['directory.example'], action: 'join' }, expect.any(AbortSignal))
    w.unmount()
  })

  it('does not fetch group or personal data while signed out', async () => {
    auth.isAuthenticated = false
    const w = setup()
    await flushPromises()
    expect(w.text()).toContain('登录后搜索群组')
    expect(groupApi.search).not.toHaveBeenCalled()
    expect(groupApi.resolve).not.toHaveBeenCalled()
    w.unmount()
  })

  it('treats remote names as text and blocks unsafe client links for encrypted groups', async () => {
    vi.mocked(groupApi.resolve).mockResolvedValue({ group: group('unsafe', { name: '<img src=x onerror=alert(1)>', membership: 'join', encryption: 'encrypted', client_url: 'javascript:alert(1)' }), target: '!unsafe:test.example', via: [] })
    const w = setup('#unsafe:test.example')
    await flushPromises()
    expect(w.text()).toContain('<img src=x onerror=alert(1)>')
    expect(w.text()).toContain('请使用 Matrix 客户端')
    expect(w.find('a[href^="javascript:"]').exists()).toBe(false)
    expect(w.find('img[onerror]').exists()).toBe(false)
    w.unmount()
  })
})
