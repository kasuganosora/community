import { afterEach, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import PostContent from '../PostContent.vue'
import { useNyaizeDisplay } from '@/shared/composables/useNyaizeDisplay'
import type { MastodonStatus } from '@/shared/types/mastodon'

const status = (account: Record<string, unknown>) => ({
  id: '1', content: '<p>banana <a class="mention" href="/@nana">@nana</a></p>', spoiler_text: '', sensitive: false, media_attachments: [], emojis: [], mentions: [], tags: [],
  account: { id: '2', username: 'neko', acct: 'neko', display_name: 'Neko', ...account },
}) as unknown as MastodonStatus

function render(s: MastodonStatus) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })
  return mount(PostContent, { props: { status: s }, global: { plugins: [createPinia(), router] } })
}

afterEach(() => useNyaizeDisplay().setEnabled(true))

it('nyaizes cats that speak as cats, at display time only', () => {
  const s = status({ is_cat: true, speak_as_cat: true })
  const w = render(s)
  expect(w.text()).toContain('banyanya')
  expect(w.text()).toContain('@nana')
  expect(s.content).toContain('banana')
  expect(render(status({ is_cat: true, speak_as_cat: false })).text()).toContain('banana ')
  expect(render(status({ is_cat: false, speak_as_cat: true })).text()).toContain('banana ')
})

it('respects the viewer setting', () => {
  useNyaizeDisplay().setEnabled(false)
  expect(render(status({ is_cat: true, speak_as_cat: true })).text()).toContain('banana ')
})
