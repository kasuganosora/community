import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import RegisterPage from '../RegisterPage.vue'
import { DEFAULT_RULES, instanceApi, type InstanceInfo } from '@/domains/instance/infrastructure/instanceApi'

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useRoute: () => ({ query: {} }),
}))

const stubs = {
  TButton: { props: ['disabled'], template: '<button :disabled="disabled"><slot /></button>' },
  TInput: { template: '<div><slot name="prefixIcon" /><slot name="suffix" /></div>' },
  TForm: { template: '<form><slot /></form>' },
  TFormItem: { template: '<div><slot /></div>' },
  TIcon: true,
}

let wrapper: VueWrapper | undefined

beforeEach(() => {
  vi.spyOn(instanceApi, 'getRules').mockResolvedValue([{ id: 'rule', text: 'Instance-specific rule' }])
  vi.spyOn(instanceApi, 'getInstance').mockResolvedValue({ domain: 'social.example' })
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
})

async function openForm() {
  wrapper = shallowMount(RegisterPage, { global: { stubs } })
  await flushPromises()
  for (const checkbox of wrapper.findAll('input[type="checkbox"]')) {
    await checkbox.setValue(true)
  }
  await wrapper.get('.btn-continue').trigger('click')
  return wrapper
}

describe('registration instance identity', () => {
  it.each<[InstanceInfo, string]>([
    [{ domain: 'social.example:8443', uri: 'wrong.example' }, 'social.example:8443'],
    [{ domain: 'https://social.example:8443/about', uri: 'wrong.example' }, 'social.example:8443'],
    [{ uri: 'legacy.example:8090' }, 'legacy.example:8090'],
    [{ uri: 'https://legacy.example:8090/about' }, 'legacy.example:8090'],
  ])('shows canonical domain/legacy uri host, preserving the port: %j', async (metadata, host) => {
    vi.mocked(instanceApi.getInstance).mockResolvedValue(metadata)
    const page = await openForm()
    expect(page.get('.input-suffix').text()).toBe(`@${host}`)
    expect(page.get('.input-suffix').attributes('title')).toBe(`@${host}`)
  })

  it('falls back to the current browser host when instance metadata fails', async () => {
    vi.mocked(instanceApi.getInstance).mockRejectedValue(new Error('Instance unavailable'))
    const page = await openForm()
    expect(page.get('.input-suffix').text()).toBe(`@${window.location.host}`)
  })

  it('keeps the browser host for malformed metadata instead of displaying an invalid address', async () => {
    vi.mocked(instanceApi.getInstance).mockResolvedValue({ domain: 'invalid host:broken' })
    const page = await openForm()
    expect(page.get('.input-suffix').text()).toBe(`@${window.location.host}`)
  })

  it('still loads instance identity when the rules endpoint fails', async () => {
    vi.mocked(instanceApi.getRules).mockRejectedValue(new Error('Rules unavailable'))
    vi.mocked(instanceApi.getInstance).mockResolvedValue({ domain: 'independent.example:9443' })
    wrapper = shallowMount(RegisterPage, { global: { stubs } })
    await flushPromises()
    expect(wrapper.findAll('.rule-text').map(rule => rule.text())).toEqual(DEFAULT_RULES.map(rule => rule.text))
    for (const checkbox of wrapper.findAll('input[type="checkbox"]')) {
      await checkbox.setValue(true)
    }
    await wrapper.get('.btn-continue').trigger('click')
    expect(wrapper.get('.input-suffix').text()).toBe('@independent.example:9443')
  })
})
