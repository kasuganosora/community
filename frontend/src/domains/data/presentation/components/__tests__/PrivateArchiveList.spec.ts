import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import PrivateArchiveList from '../PrivateArchiveList.vue'
import { archiveApi, type ArchivedPost } from '../../../infrastructure/archiveApi'
import { invalidateSessionData } from '@/shared/api/sessionScope'
const post = (id: string): ArchivedPost => ({ id, source_id: 'source-'+id, author_id: 'remote-original-author', text: 'Received private message', html: '', spoiler: '', created_at: '2026-10-03T00:00:00Z', source_uri: 'javascript:alert(1)', private_archive: true, media: [] })
const options = () => ({ global: { plugins: [createRouter({ history: createMemoryHistory(), routes: [] })] } })
afterEach(() => vi.restoreAllMocks())
describe('private backup reader', () => {
 it('reads received-message content without impersonating the author, sanitizes markup and uses server pagination', async () => {
  const list = vi.spyOn(archiveApi, 'list').mockResolvedValueOnce({ items: [{ ...post('9'), html: '<p>Private received text</p><script>bad()</script><img src=x onerror=bad()>' }], next_max_id: '7' }).mockResolvedValueOnce({ items: [post('9'), post('6')], next_max_id: null })
  const w = mount(PrivateArchiveList, options()); await flushPromises()
  expect(w.text()).toContain('Private received text'); expect(w.text()).toContain('仅你可见'); expect(w.find('script').exists()).toBe(false); expect(w.find('img').exists()).toBe(false); expect(w.find('a').exists()).toBe(false)
  await w.findAll('button').find(b => b.text() === '加载更多备份')!.trigger('click'); await flushPromises()
  expect(list.mock.calls[1]![0]).toBe('7'); expect(w.findAll('article')).toHaveLength(2); expect(w.text()).toContain('Received private message'); w.unmount()
 })
 it('erases private messages when account changes and discards an outstanding prior-account page', async () => {
  let resolve!: (value: { items: ArchivedPost[]; next_max_id: null }) => void
  vi.spyOn(archiveApi, 'list').mockResolvedValueOnce({ items: [post('9')], next_max_id: '8' }).mockImplementationOnce(() => new Promise(r => { resolve = r }))
  const w = mount(PrivateArchiveList, options()); await flushPromises(); await w.findAll('button').find(b => b.text() === '加载更多备份')!.trigger('click')
  invalidateSessionData(); resolve({ items: [post('7')], next_max_id: null }); await flushPromises(); expect(w.findAll('article')).toHaveLength(0); expect(w.text()).not.toContain('Received private message'); w.unmount()
 })
 it('does not request archived attachment URLs automatically and clears content after an authorization denial', async () => {
  vi.spyOn(archiveApi, 'list').mockResolvedValue({ items: [{ ...post('9'), media: [{ source_id: 'm1', id: '3', available: true, name: 'private.png' }] }], next_max_id: null })
  const media = vi.spyOn(archiveApi, 'media').mockRejectedValue(Object.assign(new Error('没有权限'), { response: { status: 403 } }))
  const w = mount(PrivateArchiveList, options()); await flushPromises(); expect(media).not.toHaveBeenCalled(); await w.findAll('button').find(b => b.text() === '显示附件')!.trigger('click'); await flushPromises()
  expect(w.findAll('article')).toHaveLength(0); expect(w.get('[role=alert]').text()).toContain('没有权限'); w.unmount()
 })
})
