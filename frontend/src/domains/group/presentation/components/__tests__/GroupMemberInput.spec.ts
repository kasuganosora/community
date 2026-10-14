import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { invalidateSessionData } from '@/shared/api/sessionScope'
import type { GroupInvitee } from '../../../domain/group'
import { groupApi } from '../../../infrastructure/groupApi'
import GroupMemberInput from '../GroupMemberInput.vue'

vi.mock('../../../infrastructure/groupApi', () => ({ groupApi: { invitees: vi.fn() } }))
vi.mock('../GroupAvatar.vue', () => ({ default: { template: '<span />' } }))

const alice: GroupInvitee = { user_id: '@alice:community.test', username: 'alice', display_name: 'Alice', avatar_url: '' }
const bob: GroupInvitee = { user_id: '@bob:community.test', username: 'bob', display_name: 'Bob', avatar_url: '' }
const carol: GroupInvitee = { user_id: '@carol:community.test', username: 'carol', display_name: 'Carol', avatar_url: '' }
const mounted: Array<{ unmount(): void }> = []

function setup(props: { modelValue?: string; multiple?: boolean; exclude?: string[] } = {}) {
  const host = document.createElement('form')
  document.body.append(host)
  const wrapper = mount(GroupMemberInput, {
    attachTo: host,
    props: {
      modelValue: '', ...props,
      'onUpdate:modelValue': (value: string) => { void wrapper.setProps({ modelValue: value }) },
    },
  })
  mounted.push(wrapper)
  return { wrapper, input: wrapper.get('input'), host }
}

function key(input: HTMLInputElement, name: string, extra: KeyboardEventInit = {}) {
  const event = new KeyboardEvent('keydown', { key: name, bubbles: true, cancelable: true, ...extra })
  input.dispatchEvent(event)
  return event
}

async function finishSearch() {
  await vi.advanceTimersByTimeAsync(250)
  await flushPromises()
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.resetAllMocks()
  vi.mocked(groupApi.invitees).mockResolvedValue([])
})
afterEach(() => {
  for (const wrapper of mounted.splice(0)) wrapper.unmount()
  document.body.replaceChildren()
  vi.useRealTimers()
})

describe('group member account suggestions', () => {
  it('debounces typing and ignores a previous response even when cancellation arrives too late', async () => {
    let finishOld!: (users: GroupInvitee[]) => void
    vi.mocked(groupApi.invitees).mockReturnValueOnce(new Promise(resolve => { finishOld = resolve }))
    vi.mocked(groupApi.invitees).mockResolvedValueOnce([bob])
    const { wrapper, input } = setup()
    await input.trigger('focus')
    await input.setValue('a')
    await vi.advanceTimersByTimeAsync(150)
    await input.setValue('al')
    await vi.advanceTimersByTimeAsync(249)
    expect(groupApi.invitees).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    const oldSignal = vi.mocked(groupApi.invitees).mock.calls[0]![1]!
    expect(vi.mocked(groupApi.invitees).mock.calls[0]![0]).toBe('al')

    await input.setValue('bo')
    expect(oldSignal.aborted).toBe(true)
    await finishSearch()
    finishOld([alice])
    await flushPromises()
    expect(wrapper.findAll('[role=option]').map(option => option.text())).toEqual(['Bob@bob'])
    expect(input.element.value).toBe('bo')
  })

  it('selects a local account with the keyboard without allowing Enter to submit the invitation', async () => {
    vi.mocked(groupApi.invitees).mockResolvedValue([alice, bob])
    const { wrapper, input, host } = setup()
    const submitted = vi.fn()
    // Model the enclosing form default action; a prevented key must not submit it.
    host.addEventListener('keydown', event => {
      if (event.key === 'Enter' && !event.defaultPrevented) submitted()
    })
    await input.trigger('focus')
    await input.setValue('@alice:community.test')
    await finishSearch()
    key(input.element, 'ArrowDown')
    await nextTick()
    const event = key(input.element, 'Enter')
    await flushPromises()
    expect(event.defaultPrevented).toBe(true)
    expect(submitted).not.toHaveBeenCalled()
    expect(input.element.value).toBe('@bob')
    expect(wrapper.find('[role=listbox]').exists()).toBe(false)
    await finishSearch()
    expect(groupApi.invitees).toHaveBeenCalledTimes(1)
  })

  it('closes suggestions on Escape without closing the surrounding dialog or erasing the account', async () => {
    vi.mocked(groupApi.invitees).mockResolvedValue([alice])
    const { wrapper, input, host } = setup()
    const dialogEscape = vi.fn()
    host.addEventListener('keydown', dialogEscape)
    await input.trigger('focus')
    await input.setValue('alice')
    await finishSearch()
    expect(key(input.element, 'Escape').defaultPrevented).toBe(true)
    await nextTick()
    expect(dialogEscape).not.toHaveBeenCalled()
    expect(input.element.value).toBe('alice')
    expect(wrapper.find('[role=listbox]').exists()).toBe(false)
    key(input.element, 'ArrowDown')
    await finishSearch()
    expect(wrapper.find('[role=option]').exists()).toBe(true)
  })

  it('excludes existing and already selected members while replacing only the token at the caret', async () => {
    vi.mocked(groupApi.invitees).mockResolvedValue([alice, bob, carol])
    const original = '@alice:community.test, ca @remote:elsewhere.test'
    const { wrapper, input } = setup({ multiple: true, modelValue: original, exclude: [bob.user_id] })
    const caret = original.indexOf(' ca ') + 3
    input.element.setSelectionRange(caret, caret)
    await input.trigger('focus')
    await finishSearch()
    expect(vi.mocked(groupApi.invitees).mock.calls[0]![0]).toBe('ca')
    expect(wrapper.findAll('[role=option]').map(option => option.text())).toEqual(['Carol@carol'])
    await wrapper.get('[role=option]').trigger('click')
    await flushPromises()
    expect(input.element.value).toBe('@alice:community.test, @carol @remote:elsewhere.test')
    expect(input.element.selectionStart).toBe('@alice:community.test, @carol'.length)
  })

  it('keeps external Matrix IDs usable when suggestions are empty or unavailable', async () => {
    const { wrapper, input } = setup()
    await input.trigger('focus')
    await input.setValue('@remote:elsewhere.test')
    await finishSearch()
    expect(wrapper.get('[role=status]').text()).toContain('外部 Matrix 账号可直接输入')
    expect(key(input.element, 'Enter').defaultPrevented).toBe(false)
    expect(input.element.value).toBe('@remote:elsewhere.test')
    vi.mocked(groupApi.invitees).mockRejectedValueOnce(new Error('offline'))
    await input.setValue('@other:elsewhere.test')
    await finishSearch()
    expect(wrapper.get('[role=status]').text()).toContain('可继续输入完整账号并邀请')
    expect(input.element.value).toBe('@other:elsewhere.test')
    expect(key(input.element, 'Enter').defaultPrevented).toBe(false)
  })

  it('waits for composition to finish without choosing an account while confirming Chinese input', async () => {
    vi.mocked(groupApi.invitees).mockResolvedValue([alice])
    const { input } = setup()
    await input.trigger('focus')
    await input.trigger('compositionstart')
    await input.setValue('露')
    await finishSearch()
    expect(groupApi.invitees).not.toHaveBeenCalled()
    key(input.element, 'Enter', { isComposing: true })
    await nextTick()
    expect(input.element.value).toBe('露')
    await input.trigger('compositionend')
    await finishSearch()
    expect(groupApi.invitees).toHaveBeenCalledTimes(1)
    expect(vi.mocked(groupApi.invitees).mock.calls[0]![0]).toBe('露')
    key(input.element, 'Enter', { isComposing: true })
    await nextTick()
    expect(input.element.value).toBe('露')
    key(input.element, 'Enter', { keyCode: 229 })
    await nextTick()
    expect(input.element.value).toBe('露')
  })

  it('clears results and aborts pending work across session changes and unmount', async () => {
    let finishOld!: (users: GroupInvitee[]) => void
    vi.mocked(groupApi.invitees).mockReturnValueOnce(new Promise(resolve => { finishOld = resolve }))
    const { wrapper, input } = setup()
    await input.trigger('focus')
    await input.setValue('alice')
    await finishSearch()
    const oldSignal = vi.mocked(groupApi.invitees).mock.calls[0]![1]!
    invalidateSessionData()
    expect(oldSignal.aborted).toBe(true)
    finishOld([alice])
    await flushPromises()
    expect(wrapper.find('[role=listbox]').exists()).toBe(false)

    await input.setValue('bob')
    wrapper.unmount()
    mounted.splice(mounted.indexOf(wrapper), 1)
    await finishSearch()
    expect(groupApi.invitees).toHaveBeenCalledTimes(1)

    vi.mocked(groupApi.invitees).mockReturnValueOnce(new Promise(() => {}))
    const next = setup()
    await next.input.trigger('focus')
    await next.input.setValue('carol')
    await finishSearch()
    const nextSignal = vi.mocked(groupApi.invitees).mock.calls[1]![1]!
    next.wrapper.unmount()
    mounted.splice(mounted.indexOf(next.wrapper), 1)
    expect(nextSignal.aborted).toBe(true)
  })
})
