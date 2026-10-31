import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import LlmSettingsCard from '../LlmSettingsCard.vue'
import { llmAdminApi as api, type LlmSettings } from '../../../infrastructure/llmAdminApi'

const settings: LlmSettings = {
  providers: [{ id: 'p1', name: 'Primary', type: 'openai', model: 'm', api_key_set: true, api_key_mask: 'abcd' }],
  services: { translation: 'p1' },
  provider_types: ['openai'],
  service_names: ['translation', 'new_account_mod'],
  translation: { enabled: false, mode: 'traditional', user_daily_limit: 50, daily_limit: 1000, user_per_minute: 10, allow_private: false },
}

afterEach(() => vi.restoreAllMocks())

describe('LLM service binding', () => {
  it('lists new-account moderation beside translation and saves that binding', async () => {
    vi.spyOn(api, 'get').mockResolvedValue(structuredClone(settings))
    vi.spyOn(api, 'usage').mockResolvedValue({ days: [], today: { day: '2026-10-31', translation_calls: 0 } })
    const save = vi.spyOn(api, 'save').mockImplementation(async body => ({ ...structuredClone(settings), services: body.services }))
    const w = mount(LlmSettingsCard); await flushPromises()
    const mod = w.get('[data-testid="llm-service-new_account_mod"]')
    const translation = w.get('[data-testid="llm-service-translation"]')
    expect((translation.element as HTMLSelectElement).value).toBe('p1')
    expect((mod.element as HTMLSelectElement).value).toBe('')
    expect(w.text()).toContain('用于翻译')
    await mod.setValue('p1')
    await w.get('form').trigger('submit'); await flushPromises()
    expect(save).toHaveBeenCalledWith(expect.objectContaining({
      services: { translation: 'p1', new_account_mod: 'p1' },
    }))
    expect(w.text()).toContain('用于新账号审核')
    w.unmount()
  })
})
