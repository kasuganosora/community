import { AxiosError } from 'axios'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { groupApi } from '../../../infrastructure/groupApi'
import type { Group, GroupJoinResult } from '../../../domain/group'
import { useGroupJoin } from '../useGroupJoin'

vi.mock('../../../infrastructure/groupApi', () => ({ groupApi: { join: vi.fn(), joinStatus: vi.fn() } }))
const group = { id: 'one', membership: 'invite' } as Group
function setup() {
  const id = ref('one'), apply = vi.fn()
  let joining!: ReturnType<typeof useGroupJoin>
  const wrapper = mount(defineComponent({ setup() { joining = useGroupJoin(id, apply); return {} }, template: '<div />' }))
  return { id, apply, joining, wrapper }
}
beforeEach(() => { vi.useFakeTimers(); vi.resetAllMocks(); sessionEpoch.value = 0 })
afterEach(() => vi.useRealTimers())

describe('invitation join feedback and lifecycle', () => {
  it('shows pending immediately, prevents duplicate submits, and ignores a late response after room switch', async () => {
    let resolve!: (value: GroupJoinResult) => void
    vi.mocked(groupApi.join).mockReturnValue(new Promise(done => { resolve = done }))
    const { id, apply, joining, wrapper } = setup()
    const request = joining.join(group)
    expect(joining.busy.value).toBe(true)
    await joining.join(group)
    expect(groupApi.join).toHaveBeenCalledTimes(1)
    const signal = vi.mocked(groupApi.join).mock.calls[0]![1]!
    id.value = 'two'
    expect(signal.aborted).toBe(true)
    expect(joining.busy.value).toBe(false)
    resolve({ status: 'joined', group }); await request
    expect(apply).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('stops after the overall time budget and retries only the existing operation', async () => {
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joining', operation_id: 'op' })
    vi.mocked(groupApi.joinStatus).mockResolvedValue({ status: 'joining', operation_id: 'op' })
    const { joining, apply, wrapper } = setup()
    await joining.join(group)
    await vi.advanceTimersByTimeAsync(120000)
    expect(joining.busy.value).toBe(false)
    expect(joining.error.value).toContain('重试查询')
    const calls = vi.mocked(groupApi.joinStatus).mock.calls.length
    await vi.advanceTimersByTimeAsync(60000)
    expect(groupApi.joinStatus).toHaveBeenCalledTimes(calls)
    vi.mocked(groupApi.joinStatus).mockResolvedValueOnce({ status: 'joined', group })
    await joining.join(group)
    expect(groupApi.join).toHaveBeenCalledTimes(1)
    expect(groupApi.joinStatus).toHaveBeenLastCalledWith('op', expect.any(AbortSignal))
    expect(apply).toHaveBeenCalledWith({ status: 'joined', group })
    expect(joining.operation.value).toBe('')
    wrapper.unmount()
  })
  it('aborts a stalled status request at the deadline and rejects its late success', async () => {
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joining', operation_id: 'op' })
    let resolve!: (value: GroupJoinResult) => void
    vi.mocked(groupApi.joinStatus).mockReturnValue(new Promise(done => { resolve = done }))
    const { joining, apply, wrapper } = setup()
    await joining.join(group); await vi.advanceTimersByTimeAsync(1500)
    const signal = vi.mocked(groupApi.joinStatus).mock.calls[0]![1]!
    await vi.advanceTimersByTimeAsync(120000)
    expect(signal.aborted).toBe(true)
    expect(joining.busy.value).toBe(false)
    resolve({ status: 'joined', group }); await flushPromises()
    expect(apply).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('retains a failed operation for status retry and cancels it on account switch', async () => {
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joining', operation_id: 'op' })
    vi.mocked(groupApi.joinStatus).mockRejectedValue(new Error('查询失败'))
    const { joining, wrapper } = setup()
    await joining.join(group); await vi.advanceTimersByTimeAsync(1500)
    expect(joining.error.value).toBe('查询失败')
    expect(joining.operation.value).toBe('op')
    expect(joining.busy.value).toBe(false)
    sessionEpoch.value++
    expect(joining.operation.value).toBe('')
    expect(joining.error.value).toBe('')
    wrapper.unmount()
  })
  it('keeps polling through a gateway timeout instead of reporting join failure', async () => {
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joining', operation_id: 'op' })
    const gateway = new AxiosError('timeout', 'ERR_BAD_RESPONSE', undefined, undefined, { status: 504, data: {}, statusText: '', headers: {}, config: {} as never })
    vi.mocked(groupApi.joinStatus).mockRejectedValueOnce(gateway).mockResolvedValueOnce({ status: 'joined', group })
    const { joining, apply, wrapper } = setup()
    await joining.join(group); await vi.advanceTimersByTimeAsync(1500)
    expect(joining.error.value).toBe('')
    expect(joining.busy.value).toBe(true)
    await vi.advanceTimersByTimeAsync(3000)
    expect(apply).toHaveBeenCalledWith({ status: 'joined', group })
    expect(joining.error.value).toBe('')
    wrapper.unmount()
  })
  it('stops polling on unmount', async () => {
    vi.mocked(groupApi.join).mockResolvedValue({ status: 'joining', operation_id: 'op' })
    const { joining, wrapper } = setup()
    await joining.join(group); wrapper.unmount()
    await vi.advanceTimersByTimeAsync(3000)
    expect(groupApi.joinStatus).not.toHaveBeenCalled()
  })
})
