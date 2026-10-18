import { afterEach, describe, expect, it } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { ref } from 'vue'
import ProfileFieldsEditor from '../ProfileFieldsEditor.vue'

const wrappers: VueWrapper[] = []
afterEach(() => { wrappers.splice(0).forEach(w => w.unmount()) })
function setup(initial = [{ name: 'A', value: 'one' }, { name: 'B', value: 'two' }, { name: 'C', value: 'three' }]) {
  const fields = ref(initial)
  const wrapper = mount(ProfileFieldsEditor, { props: {
    modelValue: fields.value,
    'onUpdate:modelValue': next => { fields.value = next; void wrapper.setProps({ modelValue: fields.value }) },
  } })
  wrappers.push(wrapper)
  return { wrapper, fields }
}
describe('profile fields editor', () => {
  it('edits raw text without mutating the source or replacing focused input nodes', async () => {
    const source = [{ name: 'Website', value: '<b>literal text</b>' }]
    const { wrapper, fields } = setup(source)
    const input = wrapper.get('.profile-fields__value input')
    expect(input.element).toHaveProperty('value', '<b>literal text</b>')
    const original = input.element
    await input.setValue('https://example.test/me?a=1&b=2')
    expect(wrapper.get('.profile-fields__value input').element).toBe(original)
    expect(fields.value[0]?.value).toBe('https://example.test/me?a=1&b=2')
    expect(source[0]?.value).toBe('<b>literal text</b>')
  })
  it('reorders through buttons and keyboard without losing row content', async () => {
    const { wrapper, fields } = setup()
    await wrapper.findAll('[data-action=down]')[0]!.trigger('click')
    expect(fields.value.map(row => row.name)).toEqual(['B', 'A', 'C'])
    await wrapper.findAll('.profile-fields__handle')[1]!.trigger('keydown', { key: 'ArrowUp', altKey: true })
    expect(fields.value.map(row => row.name)).toEqual(['A', 'B', 'C'])
    expect(fields.value.map(row => row.value)).toEqual(['one', 'two', 'three'])
    expect(wrapper.findAll('[data-action=up]')[0]!.attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('[data-action=down]')[2]!.attributes('disabled')).toBeDefined()
  })
  it('accepts only its own drag session and ignores drops while disabled', async () => {
    const { wrapper, fields } = setup()
    let data = ''
    const dataTransfer = { effectAllowed: '', setData: (_type: string, value: string) => { data = value }, getData: () => data }
    await wrapper.findAll('.profile-fields__row')[2]!.trigger('drop', { dataTransfer })
    expect(fields.value.map(row => row.name)).toEqual(['A', 'B', 'C'])
    await wrapper.findAll('.profile-fields__handle')[0]!.trigger('dragstart', { dataTransfer })
    await wrapper.findAll('.profile-fields__row')[2]!.trigger('drop', { dataTransfer })
    expect(fields.value.map(row => row.name)).toEqual(['B', 'C', 'A'])
    await wrapper.findAll('.profile-fields__handle')[0]!.trigger('dragstart', { dataTransfer })
    await wrapper.setProps({ disabled: true })
    await wrapper.findAll('.profile-fields__row')[2]!.trigger('drop', { dataTransfer })
    expect(fields.value.map(row => row.name)).toEqual(['B', 'C', 'A'])
  })
  it('preserves imported fields, limits additions to sixteen, and allows deletion and reuse', async () => {
    const { wrapper, fields } = setup(Array.from({ length: 16 }, (_, index) => ({ name: String(index), value: `value ${index}` })))
    expect(wrapper.findAll('.profile-fields__row')).toHaveLength(16)
    expect(wrapper.get('.profile-fields__add').attributes('disabled')).toBeDefined()
    await wrapper.findAll('[data-action=remove]')[4]!.trigger('click')
    expect(fields.value).toHaveLength(15)
    expect(fields.value[4]?.name).toBe('5')
    await wrapper.get('.profile-fields__add').trigger('click')
    expect(fields.value).toHaveLength(16)
    expect(fields.value[15]).toEqual({ name: '', value: '' })
  })
})
