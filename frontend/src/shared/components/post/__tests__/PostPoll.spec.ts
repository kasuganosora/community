import { afterEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { i18n } from '@/shared/i18n'
import PostPoll from '../PostPoll.vue'
import type { MastodonPoll } from '@/shared/types/mastodon'

const files = import.meta.glob<{ default: { shared: { PostPoll: Record<string, string> } } }>('@/locales/*.json', { eager: true })
const voteLabel = Object.fromEntries(Object.entries(files).map(([p, m]) => [p.replace(/^.*\/(.+)\.json$/, '$1'), m.default.shared.PostPoll.s2]))

const poll: MastodonPoll = {
  id: '7', expires_at: null, expired: false, multiple: false, votes_count: 0, voters_count: 0,
  voted: false, own_votes: [], options: [{ title: 'Yes', votes_count: 0 }, { title: 'No', votes_count: 0 }], emojis: [],
} as unknown as MastodonPoll

const locale = i18n.global.locale as unknown as { value: string }
const initial = locale.value
afterEach(() => { locale.value = initial })

describe('PostPoll vote button', () => {
  it('uses the verb "vote" in every locale, not the noun "poll"', () => {
    expect(voteLabel).toEqual({
      en: 'Vote',
      de: 'Abstimmen',
      fr: 'Voter',
      ru: 'Голосовать',
      ja: '投票',
      'zh-CN': '投票',
      'zh-TW': '投票',
    })
  })

  it('reads "Vote" in English', () => {
    locale.value = 'en'
    const w = mount(PostPoll, { props: { poll } })
    expect(w.find('.post-poll__footer .t-button').text()).toBe('Vote')
  })
})
