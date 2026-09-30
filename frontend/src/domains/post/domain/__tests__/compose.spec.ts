import { describe, it, expect } from 'vitest'
import type { MastodonStatus } from '@/shared/types/mastodon'
import {
  buildCreateParams, canBoost, canShare, composeLength, replyMentions, replyVisibility, validateDraft, visibilityChoices,
  type ComposeDraft,
} from '../compose'

const draft = (over: Partial<ComposeDraft> = {}): ComposeDraft => ({
  text: 'hello', spoilerEnabled: false, spoilerText: '', visibility: 'public', poll: null, mediaIds: [], ...over,
})

describe('composeLength', () => {
  it('counts the content warning when enabled', () => {
    expect(composeLength(draft({ text: 'abc', spoilerEnabled: true, spoilerText: 'cw' }))).toBe(5)
    expect(composeLength(draft({ text: 'abc', spoilerEnabled: false, spoilerText: 'cw' }))).toBe(3)
  })
  it('counts emoji and CJK characters once', () => {
    expect(composeLength(draft({ text: '你好🎉' }))).toBe(3)
  })
})

describe('validateDraft', () => {
  it('rejects empty posts but allows media-only posts', () => {
    expect(validateDraft(draft({ text: '   ' }), 500)).toBeTruthy()
    expect(validateDraft(draft({ text: '', mediaIds: ['1'] }), 500)).toBeNull()
  })
  it('enforces the character limit including the CW', () => {
    expect(validateDraft(draft({ text: 'a'.repeat(499), spoilerEnabled: true, spoilerText: 'xx' }), 500)).toMatch(/500/)
  })
  it('requires CW text when the CW is on', () => {
    expect(validateDraft(draft({ spoilerEnabled: true, spoilerText: ' ' }), 500)).toBeTruthy()
  })
  it('validates poll options', () => {
    const poll = (options: string[]) => ({ options, expiresIn: 3600, multiple: false })
    expect(validateDraft(draft({ poll: poll(['a', '']) }), 500)).toMatch(/不能为空/)
    expect(validateDraft(draft({ poll: poll(['a', 'a']) }), 500)).toMatch(/重复/)
    expect(validateDraft(draft({ poll: poll(['a']) }), 500)).toMatch(/至少/)
    expect(validateDraft(draft({ poll: poll(['a', 'b']), mediaIds: ['1'] }), 500)).toMatch(/媒体/)
    expect(validateDraft(draft({ poll: poll(['a', 'b']) }), 500)).toBeNull()
  })
})

describe('buildCreateParams', () => {
  it('sends visibility, CW, reply target, media and poll', () => {
    expect(buildCreateParams(draft({
      text: 't', visibility: 'private', spoilerEnabled: true, spoilerText: ' spoiler ', inReplyToId: '9',
      poll: { options: [' a ', 'b'], expiresIn: 300, multiple: true },
    }))).toEqual({
      status: 't', visibility: 'private', spoiler_text: 'spoiler', sensitive: true, in_reply_to_id: '9',
      poll: { options: ['a', 'b'], expires_in: 300, multiple: true },
    })
  })
  it('omits optional fields that are not in use', () => {
    expect(buildCreateParams(draft({ spoilerText: 'ignored' }))).toEqual({ status: 'hello', visibility: 'public' })
    expect(buildCreateParams(draft({ mediaIds: ['4', '5'] })).media_ids).toEqual(['4', '5'])
  })
})

describe('replies', () => {
  const parent = {
    account: { acct: 'bob' },
    mentions: [{ acct: 'alice' }, { acct: 'carol@remote.social' }, { acct: 'Bob' }],
    visibility: 'private',
  } as unknown as MastodonStatus

  it('mentions the author and other participants but not yourself', () => {
    expect(replyMentions(parent, 'alice')).toBe('@bob @carol@remote.social ')
    expect(replyMentions({ ...parent, mentions: [] }, 'bob')).toBe('')
  })

  it('never widens the visibility of the parent', () => {
    expect(replyVisibility('private')).toBe('private')
    expect(replyVisibility('direct')).toBe('direct')
    expect(replyVisibility('public')).toBe('public')
    expect(replyVisibility(undefined)).toBe('public')
  })

  it('offers only equally private or stricter choices', () => {
    expect(visibilityChoices('public').map(v => v.value)).toEqual(['public', 'unlisted', 'private', 'direct'])
    expect(visibilityChoices('unlisted').map(v => v.value)).toEqual(['unlisted', 'private', 'direct'])
    expect(visibilityChoices('private').map(v => v.value)).toEqual(['private', 'direct'])
    expect(visibilityChoices('direct').map(v => v.value)).toEqual(['direct'])
  })
})

describe('audience actions', () => {
  it('keeps boosts off followers-only and direct posts', () => {
    expect(canBoost('public')).toBe(true)
    expect(canBoost('unlisted')).toBe(true)
    expect(canBoost('private')).toBe(false)
    expect(canBoost('direct')).toBe(false)
  })
  it('does not offer a public share for a direct message', () => {
    expect(canShare('direct')).toBe(false)
    expect(canShare('private')).toBe(true)
  })
})

describe('validateDraft direct messages', () => {
  const base = { text: '', spoilerEnabled: false, spoilerText: '', visibility: 'direct' as const, poll: null, mediaIds: [] }
  it('requires a mention for direct posts', () => {
    expect(validateDraft({ ...base, text: 'hi there' }, 500)).toMatch('私信')
    expect(validateDraft({ ...base, text: 'mail me a@b.c' }, 500)).toMatch('私信')
    expect(validateDraft({ ...base, text: '@bob 你好' }, 500)).toBeNull()
    expect(validateDraft({ ...base, text: 'hi @bob@remote.social' }, 500)).toBeNull()
  })
})
