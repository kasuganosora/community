import { AxiosError } from 'axios'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, reactive } from 'vue'
import { groupPath, type Group, type GroupJoinResult } from '@/domains/group/domain/group'
import { groupApi } from '@/domains/group/infrastructure/groupApi'
import { sessionEpoch } from '@/shared/api/sessionScope'
import GroupSearchCard from '../GroupSearchCard.vue'

const auth = reactive({ isAuthenticated: true })
vi.mock('@/domains/auth/presentation/stores/authStore', () => ({ useAuthStore: () => auth }))
vi.mock('@/domains/group/infrastructure/groupApi', () => ({ groupApi: { resolve: vi.fn(), join: vi.fn(), joinStatus: vi.fn() } }))
const linkStub = defineComponent({ props: { to: { type: [String, Object], required: true } }, template: '<a :data-to="JSON.stringify(to)"><slot /></a>' })
const group = (extra: Partial<Group> = {}): Group => ({
  id: '!design:example.test', name: '设计交流', topic: '交流设计', membership: 'none', encryption: 'unencrypted', join_rule: 'public', listed: true,
  member_count: 4, unread_count: 0, client_url: 'https://matrix.to/#/!design:example.test',
  capabilities: { send_message: false, invite: false, kick: false, ban: false, redact: false, set_name: false, set_avatar: false, manage_permissions: false }, ...extra,
})
const setup = (props: InstanceType<typeof GroupSearchCard>['$props'] = { group: group(), resolved: true }) => mount(GroupSearchCard, { props, global: { stubs: { RouterLink: linkStub } } })
const button = (w: ReturnType<typeof setup>, name: string) => w.findAll('button').find(item => item.text() === name)!
beforeEach(() => {
  vi.resetAllMocks(); auth.isAuthenticated = true; sessionEpoch.value++
  vi.mocked(groupApi.resolve).mockResolvedValue({ target: '!design:example.test', via: ['example.test'], group: group() })
  vi.mocked(groupApi.join).mockResolvedValue({ status: 'joined', group: group({ membership: 'join' }) })
})
afterEach(() => vi.useRealTimers())

describe('group actions in unified search', () => {
  it('joins only after a click and validates signed directory previews with routing hints', async () => {
    const w = setup({ group: group({ preview_ref: 'signed-preview', target: '!opaque-v12', via: ['directory.test'] }) })
    await flushPromises()
    expect(groupApi.resolve).not.toHaveBeenCalled(); expect(groupApi.join).not.toHaveBeenCalled()
    vi.mocked(groupApi.resolve).mockResolvedValue({ target: '!opaque-v12', via: ['another.test'], group: group() })
    await button(w, '加入群组').trigger('click'); await flushPromises()
    expect(groupApi.resolve).toHaveBeenCalledWith('!opaque-v12', expect.any(AbortSignal), ['directory.test'], 'signed-preview')
    expect(groupApi.join).toHaveBeenCalledWith({ target: '!opaque-v12', via: ['directory.test', 'another.test'], action: 'join' }, expect.any(AbortSignal))
    expect(JSON.parse(w.get('a[data-to]').attributes('data-to')!)).toBe(groupPath('!design:example.test'))
    expect(w.text()).toContain('已加入群组'); w.unmount()
  })
  it('joins a resolved address with no metadata without a second preview flow', async () => {
    const w = setup({ target: '!opaque-v12', via: ['example.test'], clientUrl: 'https://matrix.to/#/!opaque-v12?via=example.test', resolved: true })
    expect(w.text()).toContain('若群组已加密')
    expect(w.findAll('.group-result__actions button')).toHaveLength(1)
    expect(w.find('a[href]').exists()).toBe(false); expect(groupApi.join).not.toHaveBeenCalled()
    await button(w, '加入群组').trigger('click'); await flushPromises()
    expect(groupApi.resolve).not.toHaveBeenCalled()
    expect(groupApi.join).toHaveBeenCalledWith({ target: '!opaque-v12', via: ['example.test'], action: 'join' }, expect.any(AbortSignal)); w.unmount()
  })
  it.each([
    ['knock', 'none', '申请加入', 'knock'], ['knock_restricted', 'none', '申请加入', 'knock'], ['invite', 'invite', '接受邀请', 'join'],
  ] as const)('uses the correct explicit action for %s / %s', async (join_rule, membership, label, action) => {
    const w = setup({ group: group({ join_rule, membership }), resolved: true })
    expect(w.findAll('.group-result__actions button')).toHaveLength(1)
    vi.mocked(groupApi.join).mockResolvedValue(action === 'knock' ? { status: 'knock' } : { status: 'joined', group: group({ membership: 'join' }) })
    await button(w, label).trigger('click'); await flushPromises()
    expect(groupApi.join).toHaveBeenCalledWith(expect.objectContaining({ action }), expect.any(AbortSignal))
    if (action === 'knock') { expect(w.text()).toContain('等待群管理员批准'); expect(w.find('button').exists()).toBe(false) }
    w.unmount()
  })
  it.each([{ membership: 'ban' }, { membership: 'knock' }, { join_rule: 'invite' }] as Partial<Group>[])('does not offer a join mutation for %j', extra => {
    const w = setup({ group: group(extra), resolved: true })
    expect(w.find('button').exists()).toBe(false); expect(groupApi.join).not.toHaveBeenCalled(); w.unmount()
  })
  it('honors refreshed membership without a duplicate mutation', async () => {
    const w = setup({ group: group() })
    vi.mocked(groupApi.resolve).mockResolvedValue({ target: '!design:example.test', via: [], group: group({ membership: 'join' }) })
    await button(w, '加入群组').trigger('click'); await flushPromises()
    expect(groupApi.join).not.toHaveBeenCalled(); expect(w.text()).toContain('打开群聊'); w.unmount()
  })
  it('polls an asynchronous join then presents its joined destination', async () => {
    vi.useFakeTimers()
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joining', operation_id: 'operation-1' })
    vi.mocked(groupApi.joinStatus).mockResolvedValueOnce({ status: 'joining', operation_id: 'operation-1' }).mockResolvedValueOnce({ status: 'joined', group: group({ membership: 'join' }) })
    const w = setup()
    await button(w, '加入群组').trigger('click'); await flushPromises()
    expect(w.text()).toContain('正在加入群组')
    await vi.advanceTimersByTimeAsync(3000); await flushPromises()
    expect(groupApi.join).toHaveBeenCalledTimes(1); expect(groupApi.joinStatus).toHaveBeenCalledTimes(2)
    expect(w.text()).toContain('打开群聊'); w.unmount()
  })
  it('retries status polling after failure without posting another join', async () => {
    vi.useFakeTimers()
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joining', operation_id: 'operation-1' })
    vi.mocked(groupApi.joinStatus).mockRejectedValueOnce(new Error('状态查询超时')).mockResolvedValueOnce({ status: 'joined', group: group({ membership: 'join' }) })
    const w = setup()
    await button(w, '加入群组').trigger('click'); await flushPromises()
    await vi.advanceTimersByTimeAsync(1500); await flushPromises()
    expect(w.get('[role="alert"]').text()).toContain('状态查询超时')
    await button(w, '继续检查加入状态').trigger('click'); await flushPromises()
    expect(groupApi.join).toHaveBeenCalledTimes(1); expect(groupApi.joinStatus).toHaveBeenCalledTimes(2)
    expect(w.text()).toContain('打开群聊'); w.unmount()
  })
  it.each([504, 429, undefined])('keeps checking the existing join after a transient %s response', async status => {
    vi.useFakeTimers()
    const failure = new AxiosError('暂时不可用', 'ERR_NETWORK', undefined, undefined, status ? { status, data: {}, statusText: '', headers: {}, config: {} as never } : undefined)
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joining', operation_id: 'operation-1' })
    vi.mocked(groupApi.joinStatus).mockRejectedValueOnce(failure).mockResolvedValueOnce({ status: 'joined', group: group({ membership: 'join' }) })
    const w = setup()
    await button(w, '加入群组').trigger('click'); await flushPromises()
    await vi.advanceTimersByTimeAsync(1500)
    expect(w.find('[role="alert"]').exists()).toBe(false)
    expect(w.get('[role="status"]').text()).toContain('仍在确认加入结果')
    expect(w.get('button').attributes('disabled')).toBeDefined()
    await vi.advanceTimersByTimeAsync(3000)
    expect(groupApi.join).toHaveBeenCalledTimes(1)
    expect(groupApi.joinStatus).toHaveBeenLastCalledWith('operation-1', expect.any(AbortSignal))
    expect(w.text()).toContain('打开群聊')
    w.unmount()
  })
  it('stops retries for an explicitly non-retryable status error', async () => {
    vi.useFakeTimers()
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joining', operation_id: 'operation-1' })
    const failure = new AxiosError('加入操作已结束', 'ERR_BAD_RESPONSE', undefined, undefined, { status: 503, data: { retryable: false }, statusText: '', headers: {}, config: {} as never })
    vi.mocked(groupApi.joinStatus).mockRejectedValue(failure)
    const w = setup()
    await button(w, '加入群组').trigger('click'); await flushPromises()
    await vi.advanceTimersByTimeAsync(1500)
    expect(w.get('[role="alert"]').text()).toBe('加入操作已结束')
    await vi.advanceTimersByTimeAsync(120000)
    expect(groupApi.joinStatus).toHaveBeenCalledTimes(1)
    w.unmount()
  })
  it('bounds repeated transient errors and resumes status without posting another join', async () => {
    vi.useFakeTimers()
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joining', operation_id: 'operation-1' })
    vi.mocked(groupApi.joinStatus).mockRejectedValue(new AxiosError('offline', 'ERR_NETWORK'))
    const w = setup()
    await button(w, '加入群组').trigger('click'); await flushPromises()
    await vi.advanceTimersByTimeAsync(120000)
    expect(w.get('[role="alert"]').text()).toContain('继续检查状态')
    const calls = vi.mocked(groupApi.joinStatus).mock.calls.length
    await vi.advanceTimersByTimeAsync(10000)
    expect(groupApi.joinStatus).toHaveBeenCalledTimes(calls)
    vi.mocked(groupApi.joinStatus).mockResolvedValueOnce({ status: 'joined', group: group({ membership: 'join' }) })
    await button(w, '继续检查加入状态').trigger('click'); await flushPromises()
    expect(groupApi.join).toHaveBeenCalledTimes(1)
    expect(groupApi.joinStatus).toHaveBeenLastCalledWith('operation-1', expect.any(AbortSignal))
    expect(w.text()).toContain('打开群聊'); w.unmount()
  })
  it('retries a failed join without claiming success', async () => {
    vi.mocked(groupApi.join).mockRejectedValueOnce(new Error('暂时无法加入'))
    const w = setup()
    await button(w, '加入群组').trigger('click'); await flushPromises()
    expect(w.get('[role="alert"]').text()).toContain('暂时无法加入'); expect(w.text()).not.toContain('已加入群组')
    await button(w, '重试').trigger('click'); await flushPromises()
    expect(groupApi.join).toHaveBeenCalledTimes(2); expect(w.text()).toContain('打开群聊'); w.unmount()
  })
  it('bounds a stalled status request and resumes its operation without a second join', async () => {
    vi.useFakeTimers()
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joining', operation_id: 'slow-operation' })
    vi.mocked(groupApi.joinStatus).mockImplementationOnce(() => new Promise(() => {}))
    const w = setup()
    await button(w, '加入群组').trigger('click'); await flushPromises()
    await vi.advanceTimersByTimeAsync(120000); await flushPromises()
    expect(w.get('[role="alert"]').text()).toContain('继续检查状态')
    expect(vi.mocked(groupApi.joinStatus).mock.calls[0]![1]!.aborted).toBe(true)
    vi.mocked(groupApi.joinStatus).mockResolvedValueOnce({ status: 'joined', group: group({ membership: 'join' }) })
    await button(w, '继续检查加入状态').trigger('click'); await flushPromises()
    expect(groupApi.join).toHaveBeenCalledTimes(1)
    expect(groupApi.joinStatus).toHaveBeenLastCalledWith('slow-operation', expect.any(AbortSignal))
    expect(w.text()).toContain('打开群聊'); w.unmount()
  })
  it('cancels a pending join and ignores its result when authenticated identity changes', async () => {
    let complete!: (result: GroupJoinResult) => void
    vi.mocked(groupApi.join).mockReturnValue(new Promise(resolve => { complete = resolve }))
    const w = setup()
    await button(w, '加入群组').trigger('click')
    const signal = vi.mocked(groupApi.join).mock.calls[0]![1]!
    sessionEpoch.value++; expect(signal.aborted).toBe(true)
    complete({ status: 'joined', group: group({ name: '旧用户私密群', membership: 'join' }) }); await flushPromises()
    expect(w.text()).not.toContain('旧用户私密群'); expect(w.text()).not.toContain('打开群聊'); w.unmount()
  })
  it('does not submit a join after an obsolete directory preview completes', async () => {
    let complete!: (result: Awaited<ReturnType<typeof groupApi.resolve>>) => void
    vi.mocked(groupApi.resolve).mockReturnValue(new Promise(resolve => { complete = resolve }))
    const w = setup({ group: group() })
    await button(w, '加入群组').trigger('click')
    const signal = vi.mocked(groupApi.resolve).mock.calls[0]![1]!
    await w.setProps({ group: group({ id: '!other:example.test' }) })
    expect(signal.aborted).toBe(true)
    complete({ target: '!design:example.test', via: [] }); await flushPromises()
    expect(groupApi.join).not.toHaveBeenCalled(); w.unmount()
  })
  it('stops scheduled polling when results change or the card unmounts', async () => {
    vi.useFakeTimers()
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joining', operation_id: 'operation-1' })
    const w = setup()
    await button(w, '加入群组').trigger('click'); await flushPromises()
    await w.setProps({ group: group({ id: '!other:example.test' }) }); await vi.advanceTimersByTimeAsync(1500)
    expect(groupApi.joinStatus).not.toHaveBeenCalled()
    await button(w, '加入群组').trigger('click'); await flushPromises()
    w.unmount(); await vi.advanceTimersByTimeAsync(1500); expect(groupApi.joinStatus).not.toHaveBeenCalled()
  })
  it('hands unsupported/encrypted rooms to an external client without offering web chat', async () => {
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joined', client_required: true, client_url: 'https://matrix.to/#/!encrypted:example.test' })
    const w = setup({ group: group({ encryption: 'encrypted' }), resolved: true })
    expect(w.text()).toContain('加密群无法在网页收发消息')
    await button(w, '加入并使用客户端').trigger('click'); await flushPromises()
    expect(w.get('a[href]').attributes('href')).toBe('https://matrix.to/#/!encrypted:example.test')
    expect(w.find('a[data-to]').exists()).toBe(false); expect(w.text()).toContain('需要使用 Matrix 客户端'); w.unmount()
  })
})
