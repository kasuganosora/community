import { describe, expect, it } from 'vitest'
import { needsTranslation, visibilityAllowsTranslation } from '../useStatusTranslation'

describe('needsTranslation', () => {
  it('offers translation only when the primary language differs', () => {
    expect(needsTranslation('ja', 'en')).toBe(true)
    expect(needsTranslation('en-GB', 'en')).toBe(false)
    expect(needsTranslation('zh', 'zh-TW')).toBe(false)
    expect(needsTranslation('de', 'zh-CN')).toBe(true)
    expect(needsTranslation(null, 'en')).toBe(false)
    expect(needsTranslation('und', 'en')).toBe(false)
  })
})

describe('visibilityAllowsTranslation', () => {
  it('limits restricted instances to public and unlisted posts', () => {
    expect(visibilityAllowsTranslation('public', true)).toBe(true)
    expect(visibilityAllowsTranslation('unlisted', true)).toBe(true)
    expect(visibilityAllowsTranslation('private', true)).toBe(false)
    expect(visibilityAllowsTranslation('direct', true)).toBe(false)
    expect(visibilityAllowsTranslation('direct', false)).toBe(true)
  })
})
