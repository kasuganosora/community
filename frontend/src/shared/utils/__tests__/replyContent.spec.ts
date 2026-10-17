import { describe, it, expect } from 'vitest'
import { withoutLeadingReplyMention } from '../replyContent'
import type { MastodonAccount } from '@/shared/types/mastodon'
const recipient = { acct: 'alice@example.test', url: 'https://example.test/@alice' } as MastodonAccount
const mention = '<span class="h-card"><a class="u-url mention" href="https://example.test/@alice">@<span>alice</span></a></span>'
describe('compact reply body', () => {
 it('removes only the exact leading recipient already shown in the reply chip', () => {
  expect(withoutLeadingReplyMention(`<p>${mention} Hello</p>`, recipient)).toBe('<p>Hello</p>')
  expect(withoutLeadingReplyMention(`<p>Hello ${mention}</p>`, recipient)).toContain(mention)
  expect(withoutLeadingReplyMention(`<p>${mention} Hello</p>`, { ...recipient, acct: 'bob@example.test', url: 'https://example.test/@bob' })).toContain(mention)
  expect(withoutLeadingReplyMention('<p>@alice plain text</p>', recipient)).toBe('<p>@alice plain text</p>')
 })
})
