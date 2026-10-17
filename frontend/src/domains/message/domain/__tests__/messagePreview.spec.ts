import { describe, expect, it } from 'vitest'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { messagePreview } from '../messagePreview'

const local = { acct: 'luna', username: 'luna', url: 'https://c.hime.at/@luna' } as MastodonAccount
describe('private message previews', () => {
  it('removes bridge-generated profile URLs and redundant leading recipients', () => {
    expect(messagePreview('@luna@c.hime.at (https://c.hime.at/@luna) 再测试下', [local])).toBe('再测试下')
    expect(messagePreview('@luna@c.hime.at (https://c.hime.at/users/luna) 你好', [local])).toBe('你好')
    expect(messagePreview('@luna (https://c.hime.at/@luna) hi', [local])).toBe('hi')
  })
  it('keeps actual parentheses, URLs, unmatched mentions and mentions inside the body', () => {
    const text = '你好（明天见） (https://example.org) @bob@remote.test (https://unrelated.test/@bob)'
    expect(messagePreview(text, [local])).toBe(text)
    expect(messagePreview('Hi @luna 你好', [local])).toBe('Hi @luna 你好')
    expect(messagePreview('@luna hello', [])).toBe('@luna hello')
    expect(messagePreview('@luna@c.hime.at (https://c.hime.at/@other) hi')).toContain('https://')
    expect(messagePreview('@luna@c.hime.at (https://c.hime.at/@luna?view=posts) hi')).toContain('https://')
  })
  it('preserves mention-only messages and flattens whitespace', () => {
    expect(messagePreview('@luna', [local])).toBe('@luna')
    expect(messagePreview('hi\n there   again')).toBe('hi there again')
  })
})
