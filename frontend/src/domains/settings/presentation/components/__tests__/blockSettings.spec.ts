import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import DomainBlockSettings from '../DomainBlockSettings.vue'
import FilterSettings from '../FilterSettings.vue'
import AccountRelationList from '../AccountRelationList.vue'
import { moderationApi, nextLink, type MastodonFilter } from '../../../infrastructure/moderationApi'
import type { MastodonAccount } from '@/shared/types/mastodon'

vi.mock('@/shared/ui/feedback', () => ({ MessagePlugin: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() }, DialogPlugin: { confirm: vi.fn(), alert: vi.fn() } }))
vi.mock('tdesign-vue-next', () => ({ MessagePlugin: { error: vi.fn(), success: vi.fn() } }))
let wrappers: VueWrapper[] = []
afterEach(() => { wrappers.forEach(w => w.unmount()); wrappers = []; vi.restoreAllMocks(); vi.clearAllMocks() })

const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div />' } }] })

it('creates one filter with every non-blank, de-duplicated line as a keyword', async () => {
  vi.spyOn(moderationApi, 'listFilters').mockResolvedValue([])
  const create = vi.spyOn(moderationApi, 'createFilter').mockImplementation(async (d) => ({ id: 'n1', title: d.title, context: d.context, expires_at: null, filter_action: d.filter_action, keywords: d.keywords_attributes.map((k, i) => ({ id: `n1-${i}`, keyword: k.keyword!, whole_word: !!k.whole_word })) }))
  const w = mount(FilterSettings); wrappers.push(w); await flushPromises()
  expect(w.get('#filter-keywords').element.tagName).toBe('TEXTAREA')
  await w.get('#filter-title').setValue('Spam')
  await w.get('#filter-keywords').setValue(' buy now \n\ncheap\nbuy now\r\n  ')
  await w.get('form').trigger('submit'); await flushPromises()
  expect(create).toHaveBeenCalledTimes(1)
  expect(create.mock.calls[0]![0].keywords_attributes).toEqual([{ keyword: 'buy now', whole_word: false }, { keyword: 'cheap', whole_word: false }])
  expect(w.findAll('[data-testid=filter-item]')).toHaveLength(1)
})

it('edits a filter by showing its keywords one per line and sending only the diff', async () => {
  const existing: MastodonFilter = { id: 'f1', title: 'Old', context: ['home'], expires_at: null, filter_action: 'warn', keywords: [{ id: 'f1-a', keyword: 'keep', whole_word: false }, { id: 'f1-b', keyword: 'drop', whole_word: false }] }
  vi.spyOn(moderationApi, 'listFilters').mockResolvedValue([existing])
  const update = vi.spyOn(moderationApi, 'updateFilter').mockResolvedValue({ ...existing, keywords: [existing.keywords[0]!, { id: 'f1-c', keyword: 'new', whole_word: false }] })
  const w = mount(FilterSettings); wrappers.push(w); await flushPromises()
  await w.get('[data-testid=filter-item] button').trigger('click')
  expect((w.get('#filter-keywords').element as HTMLTextAreaElement).value).toBe('keep\ndrop')
  await w.get('#filter-keywords').setValue('keep\nnew')
  await w.get('form').trigger('submit'); await flushPromises()
  expect(update).toHaveBeenCalledWith('f1', expect.objectContaining({ title: 'Old', keywords_attributes: [{ id: 'f1-b', _destroy: true }, { keyword: 'new', whole_word: false }] }))
})

it('blocks each normalized domain line and keeps failures in the box', async () => {
  vi.spyOn(moderationApi, 'listDomainBlocks').mockResolvedValue({ items: [] })
  const block = vi.spyOn(moderationApi, 'blockDomain').mockImplementation(async (d) => { if (d === 'bad.example') throw Object.assign(new Error('x'), { response: { data: { code: 'DOMAIN_INVALID' } } }) })
  const w = mount(DomainBlockSettings); wrappers.push(w); await flushPromises()
  await w.get('#domain-block').setValue('https://Spam.Example/path\nspam.example\n\nbad.example\n')
  await w.get('form').trigger('submit'); await flushPromises()
  expect(block.mock.calls.map(c => c[0])).toEqual(['spam.example', 'bad.example'])
  expect((w.get('#domain-block').element as HTMLTextAreaElement).value).toBe('bad.example')
  expect(w.get('[role=alert]').text()).toContain('bad.example')
  expect(w.findAll('[data-testid=domain-block-item]').map(li => li.text())).toEqual([expect.stringContaining('spam.example')])
})

it('rejects lines that are not domains before sending anything', async () => {
  vi.spyOn(moderationApi, 'listDomainBlocks').mockResolvedValue({ items: [] })
  const block = vi.spyOn(moderationApi, 'blockDomain')
  const w = mount(DomainBlockSettings); wrappers.push(w); await flushPromises()
  await w.get('#domain-block').setValue('good.example\nnot a domain')
  await w.get('form').trigger('submit'); await flushPromises()
  expect(block).not.toHaveBeenCalled()
  expect(w.get('[role=alert]').text()).toContain('not a domain')
})

const account = (id: string, acct: string): MastodonAccount => ({ id, username: acct, acct, display_name: acct.toUpperCase(), avatar: '', avatar_static: '', emojis: [] } as unknown as MastodonAccount)

it('lists accounts, pages with the Link cursor, re-sorts, and undoes a mute', async () => {
  const list = vi.spyOn(moderationApi, 'listAccounts')
    .mockResolvedValueOnce({ items: [account('1', 'alice')], next: '/api/v1/mutes?max_id=9' })
    .mockResolvedValueOnce({ items: [account('2', 'bob')] })
    .mockResolvedValueOnce({ items: [account('2', 'bob'), account('1', 'alice')] })
  const undo = vi.spyOn(moderationApi, 'undo').mockResolvedValue()
  const w = mount(AccountRelationList, { props: { kind: 'mutes' }, global: { plugins: [router] } }); wrappers.push(w); await flushPromises()
  expect(w.text()).toContain('@alice')
  await w.get('.relation-list__more').trigger('click'); await flushPromises()
  expect(list).toHaveBeenLastCalledWith('mutes', 'newest', '/api/v1/mutes?max_id=9')
  expect(w.findAll('[data-testid=relation-account]')).toHaveLength(2)
  await w.findAll('.relation-order__item')[1]!.trigger('click'); await flushPromises()
  expect(list).toHaveBeenLastCalledWith('mutes', 'oldest', undefined)
  await w.get('[data-testid=relation-account] .relation-list__action').trigger('click'); await flushPromises()
  expect(undo).toHaveBeenCalledWith('mutes', '2')
  expect(w.findAll('[data-testid=relation-account]')).toHaveLength(1)
})

it('reads the next page from a Link header', () => {
  expect(nextLink('<https://c.example/api/v1/blocks?limit=40&max_id=5>; rel="next", <https://c.example/api/v1/blocks?min_id=9>; rel="prev"')).toBe('/api/v1/blocks?limit=40&max_id=5')
  expect(nextLink('<https://c.example/api/v1/blocks?min_id=9>; rel="prev"')).toBeUndefined()
  expect(nextLink(undefined)).toBeUndefined()
})
