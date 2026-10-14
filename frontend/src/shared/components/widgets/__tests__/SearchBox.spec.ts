import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import SearchBox from '../SearchBox.vue'

const router = vi.hoisted(() => ({ push: vi.fn() }))
vi.mock('vue-router', () => ({ useRouter: () => router }))
const input = defineComponent({
  props: { modelValue: String }, emits: ['update:modelValue'],
  template: '<input :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
})
const setup = (props = {}) => mount(SearchBox, { props, global: { stubs: { 't-input': input, 't-icon': true } } })
beforeEach(() => { vi.clearAllMocks(); router.push.mockResolvedValue(undefined) })
afterEach(() => vi.useRealTimers())

describe('one shared search field', () => {
  it('routes group names and empty directory searches through unified search', async () => {
    const w = setup({ searchType: 'groups', allowEmpty: true, embedded: true })
    await w.get('input').setValue('  godot  ')
    await w.get('form').trigger('submit')
    expect(router.push).toHaveBeenLastCalledWith({ name: 'search', query: { q: 'godot', type: 'groups' } })
    await w.get('input').setValue('')
    await w.get('form').trigger('submit')
    expect(router.push).toHaveBeenLastCalledWith({ name: 'search', query: { q: undefined, type: 'groups' } })
    w.unmount()
  })
  it('recognizes a room address from the global field without attempting to join', async () => {
    const w = setup()
    const address = 'https://matrix.to/#/%23godot:matrix.org?via=matrix.org'
    await w.get('input').setValue(address)
    await w.get('form').trigger('submit')
    expect(router.push).toHaveBeenCalledWith({ name: 'search', query: { q: address, type: 'groups' } })
    w.unmount()
  })
  it('lets results own refresh without creating a second navigation', async () => {
    const w = setup({ modelValue: '摄影', navigate: false, embedded: true })
    await w.get('form').trigger('submit')
    expect(w.emitted('search')).toEqual([['摄影']])
    expect(router.push).not.toHaveBeenCalled()
    await w.setProps({ modelValue: '旅行' })
    expect(w.get('input').element.value).toBe('旅行')
    w.unmount()
  })
  it('does not navigate during IME composition or repeat a submitted live search', async () => {
    vi.useFakeTimers()
    const w = setup({ live: true })
    await w.get('input').trigger('compositionstart')
    await w.get('input').setValue('摄影')
    await w.get('form').trigger('submit')
    await vi.advanceTimersByTimeAsync(450)
    expect(router.push).not.toHaveBeenCalled()
    await w.get('input').trigger('compositionend')
    await w.get('form').trigger('submit')
    await vi.advanceTimersByTimeAsync(450)
    await flushPromises()
    expect(router.push).toHaveBeenCalledTimes(1)
    w.unmount()
  })
})
