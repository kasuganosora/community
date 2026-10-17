import { describe, expect, it } from 'vitest'
import { shallowMount } from '@vue/test-utils'
import ReplyContext from '../ReplyContext.vue'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import type { MastodonReplyPreview } from '@/shared/types/mastodon'
import { deletedStatusIds } from '@/domains/post/presentation/composables/useStatusActions'
const parent = { id: 'parent', account: { id: 'a', acct: 'alice', username: 'alice' }, content: '<p>secret body</p>', spoiler_text: '', sensitive: false, emojis: [], created_at: '2026-10-17T00:00:00Z' } as unknown as MastodonReplyPreview
function summary(status: MastodonReplyPreview) {
 const wrapper = shallowMount(ReplyContext, { props: { status }, global: { renderStubDefaultSlot: true } })
 const value = wrapper.findAllComponents(EmojiText).at(-1)?.props('text')
 wrapper.unmount()
 return value
}
describe('reply context', () => {
 it('summarizes ordinary text but does not expose hidden content', () => {
  expect(summary(parent)).toBe('secret body')
  expect(summary({ ...parent, spoiler_text: 'content warning' })).toBe('content warning')
  expect(summary({ ...parent, sensitive: true })).not.toContain('secret body')
  expect(summary({ ...parent, filtered: ['hide'] })).not.toContain('secret body')
 })
 it('removes a parent deleted elsewhere in the session', () => {
  deletedStatusIds.add(parent.id)
  const wrapper = shallowMount(ReplyContext, { props: { status: parent } })
  expect(wrapper.find('.reply-context').exists()).toBe(false)
  wrapper.unmount()
  deletedStatusIds.clear()
 })
})
