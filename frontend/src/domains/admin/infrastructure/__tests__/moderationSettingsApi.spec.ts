import { describe, expect, it } from 'vitest'
import { invalidRegexLine, splitLines } from '../moderationSettingsApi'
import { adminSection } from '../../domain/navigation'

describe('moderation settings helpers', () => {
  it('splits textarea input like Misskey (one entry per line, empty lines dropped)', () => {
    expect(splitLines('a b\n\n/x/i\r\n  \nc')).toEqual(['a b', '/x/i', 'c'])
  })
  it('reports the first invalid regular expression', () => {
    expect(invalidRegexLine(['plain', '/ok+/i'])).toBeUndefined()
    expect(invalidRegexLine(['/ok/', '/(/'])).toBe('/(/')
  })
  it('registers the moderation admin page', () => {
    expect(adminSection('/admin/moderation').title).toBe('管理')
  })
})
