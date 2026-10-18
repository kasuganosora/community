import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import DiscoverySettings from '../DiscoverySettings.vue'
import { discoveryApi } from '../../../infrastructure/discoveryApi'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { discoveryPreference } from '../../composables/discoveryState'
beforeEach(() => { sessionEpoch.value++ })
afterEach(() => vi.restoreAllMocks())

it('saves the reading preference without changing account discovery and lets a user undo feedback independently', async () => {
  vi.spyOn(discoveryApi, 'preferences').mockResolvedValue({ preference: 'generic', revision: 1, enabled: true })
  vi.spyOn(discoveryApi, 'feedback').mockResolvedValue([{ id: '25', action: 'hide_tag', target_kind: 'tag', target_key: 'photography', created_at: '' }])
  const save = vi.spyOn(discoveryApi, 'setPreference').mockResolvedValue({ preference: 'off', revision: 2 })
  const undo = vi.spyOn(discoveryApi, 'removeFeedback').mockResolvedValue({ preference: 'off', revision: 3 })
  const wrapper = mount(DiscoverySettings)
  await flushPromises()
  await wrapper.get('input[value="off"]').setValue()
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(save).toHaveBeenCalledWith('off', [])
  expect(discoveryPreference.value?.preference).toBe('off')
  expect(wrapper.text()).toContain('#photography')
  await wrapper.get('li button').trigger('click'); await flushPromises()
  expect(undo).toHaveBeenCalledWith('25')
  expect(wrapper.find('li').exists()).toBe(false)
  wrapper.unmount()
})

it('does not overwrite unread preferences with the generic default after a load failure', async () => {
  vi.spyOn(discoveryApi, 'preferences').mockRejectedValue(new Error('offline'))
  vi.spyOn(discoveryApi, 'feedback').mockResolvedValue([])
  const save = vi.spyOn(discoveryApi, 'setPreference')
  const wrapper = mount(DiscoverySettings)
  await flushPromises()
  expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(save).not.toHaveBeenCalled()
  expect(wrapper.get('[role="alert"]').text()).toContain('offline')
  wrapper.unmount()
})

it('saves explicit reading languages separately from the posting language and validates custom codes', async () => {
  vi.spyOn(discoveryApi, 'preferences').mockResolvedValue({ preference: 'generic', revision: 1, languages: ['en'] })
  vi.spyOn(discoveryApi, 'feedback').mockResolvedValue([])
  const save = vi.spyOn(discoveryApi, 'setPreference').mockResolvedValue({ preference: 'generic', revision: 2, languages: ['en', 'zh-Hant'] })
  const wrapper = mount(DiscoverySettings)
  await flushPromises()
  await wrapper.get('select').setValue('zh-Hant')
  const input = wrapper.get('input[placeholder="例如 it、nl、uk"]')
  await input.setValue('not valid!')
  await input.trigger('keydown', { key: 'Enter' })
  expect(wrapper.text()).toContain('请输入标准语言代码')
  await input.setValue('')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(save).toHaveBeenCalledWith('generic', ['en', 'zh-Hant'])
  expect(wrapper.text()).toContain('不会改变发帖时的默认语言')
  wrapper.unmount()
})

it('renders local and remote usernames and can undo an unavailable account rule', async () => {
 vi.spyOn(discoveryApi, 'preferences').mockResolvedValue({ preference: 'generic', revision: 1 })
 vi.spyOn(discoveryApi, 'feedback').mockResolvedValue([
  { id:'1', action:'hide_author', target_kind:'account', target_key:'3500', target_label:'@alice', created_at:'' },
  { id:'2', action:'dismiss_suggestion', target_kind:'account', target_key:'3501', target_label:'@bob@remote.example', created_at:'' },
  { id:'3', action:'hide_author', target_kind:'account', target_key:'3502', created_at:'' },
 ])
 const undo=vi.spyOn(discoveryApi,'removeFeedback').mockResolvedValue({preference:'generic',revision:2})
 const w=mount(DiscoverySettings);await flushPromises()
 expect(w.text()).toContain('@alice');expect(w.text()).toContain('@bob@remote.example')
 expect(w.text()).toContain('账号已不可用');expect(w.text()).not.toContain('350')
 await w.findAll('.discovery-rules li button')[2]!.trigger('click');await flushPromises()
 expect(undo).toHaveBeenCalledWith('3');expect(w.findAll('.discovery-rules li')).toHaveLength(2)
 w.unmount()
})
