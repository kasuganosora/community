import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import ListTimelinePage from '../ListTimelinePage.vue'
import { listApi } from '../../../infrastructure/listApi'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { invalidateSessionData } from '@/shared/api/sessionScope'
import type { MastodonAccount, MastodonList, MastodonStatus } from '@/shared/types/mastodon'

const route = reactive({ params: { id: 'a' }, query: {} })
const replace = vi.fn().mockResolvedValue(undefined)
let refresh: () => Promise<unknown>
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ replace }) }))
vi.mock('@/shared/composables/useFeedStream', () => ({ useFeedStream: (_: unknown, callback: () => Promise<unknown>) => { refresh = callback } }))
vi.mock('@/domains/post/presentation/composables/useStatusActions', () => ({ useStatusActions: () => ({}), applyStatusUpdate: vi.fn(), editedStatuses: new Map() }))
vi.mock('@/shared/ui/feedback', () => ({ MessagePlugin: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() }, DialogPlugin: { confirm: vi.fn(), alert: vi.fn() } }))
vi.mock('tdesign-vue-next', () => ({ MessagePlugin: { success: vi.fn() }, Loading: { template: '<span />' }, Dialog: { props: ['visible'], template: '<div v-if="visible" data-dialog><slot /></div>' } }))
const member = { id: 'member', acct: 'member', display_name: 'Member' } as MastodonAccount
const candidate = { id: 'candidate', acct: 'candidate', display_name: 'Candidate' } as MastodonAccount
const fixture = (id: string) => ({ id, title: `List ${id}`, replies_policy: 'list' }) as MastodonList
const global = { stubs: {
  TimelineFeed: { props: ['statuses'], template: '<div data-feed>{{ statuses.map(s => s.id).join(",") }}</div>' },
  AccountRow: { props: ['account'], template: '<div>{{ account.id }}<slot name="action" /></div>' },
  't-dialog': { props: ['visible'], template: '<div v-if="visible" data-dialog><slot /></div>' },
  't-loading': true, 't-icon': true, 'router-link': true,
} }
function deferred<T>() { let resolve!: (v: T) => void; let reject!: (e: Error) => void; const promise = new Promise<T>((a,b) => {resolve=a;reject=b}); return {promise,resolve,reject} }
beforeEach(() => {
  vi.restoreAllMocks(); replace.mockClear(); route.params.id = 'a'; setActivePinia(createPinia())
  useAuthStore().currentUser = { id: 'alice' } as MastodonAccount
  vi.spyOn(listApi, 'getList').mockImplementation(async id => fixture(id))
  vi.spyOn(listApi, 'getTimeline').mockResolvedValue([{id:'10'} as MastodonStatus])
  vi.spyOn(listApi, 'getAccounts').mockResolvedValue([member])
  vi.spyOn(userApi, 'getAllFollowing').mockResolvedValue([candidate])
  vi.spyOn(listApi, 'getTags').mockResolvedValue([])
})
async function open() {
  const w = mount(ListTimelinePage, {global});await flushPromises()
  await w.findAll('button').find(b=>b.text()==='管理')!.trigger('click');await flushPromises();return w
}
describe('list timeline scope isolation', () => {
  it.each(['route','account'] as const)('ignores a delayed save after %s change', async kind => {
    const pending=deferred<MastodonList>();vi.spyOn(listApi,'updateList').mockReturnValue(pending.promise)
    const w=await open();await w.get('form').trigger('submit')
    if(kind==='route')route.params.id='b';else invalidateSessionData()
    await flushPromises();pending.resolve({...fixture('a'),title:'Old private title'});await flushPromises()
    expect(w.get('h1').text()).not.toContain('Old private title')
    expect(w.find('[data-dialog]').exists()).toBe(false);w.unmount()
  })
  it.each(['add','remove','delete'] as const)('ignores delayed %s completion and does not navigate away from a new list', async action => {
    const pending=deferred<void>()
    if(action==='add')vi.spyOn(listApi,'addAccounts').mockReturnValue(pending.promise)
    if(action==='remove')vi.spyOn(listApi,'removeAccounts').mockReturnValue(pending.promise)
    if(action==='delete')vi.spyOn(listApi,'deleteList').mockReturnValue(pending.promise)
    const w=await open()
    if(action==='delete')await w.findAll('button').find(b=>b.text()==='删除列表')!.trigger('click')
    await w.findAll('button').find(b=>b.text()===({add:'添加',remove:'移除',delete:'删除'}[action]))!.trigger('click')
    route.params.id='b';await flushPromises();await w.findAll('button').find(b=>b.text()==='管理')!.trigger('click');await flushPromises()
    pending.resolve();await flushPromises()
    expect(w.get('h1').text()).toBe('List b');expect(replace).not.toHaveBeenCalled()
    expect(w.text()).toContain('成员（1）');expect(w.findAll('button').find(b=>b.text()==='保存')!.attributes('disabled')).toBeUndefined();w.unmount()
  })
  it('does not let an old failure clear the new operation busy state or show its error', async () => {
    const old=deferred<MastodonList>(), current=deferred<MastodonList>()
    vi.spyOn(listApi,'updateList').mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const w=await open();await w.get('form').trigger('submit')
    route.params.id='b';await flushPromises();await w.findAll('button').find(b=>b.text()==='管理')!.trigger('click');await flushPromises()
    await w.get('form').trigger('submit');old.reject(new Error('Old private failure'));await flushPromises()
    expect(w.text()).not.toContain('Old private failure')
    expect(w.findAll('button').find(b=>b.text()==='保存')!.attributes('disabled')).toBeDefined()
    current.resolve(fixture('b'));await flushPromises();w.unmount()
  })
  it('keeps the current feed mounted when a new-content hint appears', async () => {
    const w=mount(ListTimelinePage,{global});await flushPromises()
    vi.mocked(listApi.getTimeline).mockResolvedValueOnce([{id:'20'},{id:'10'}] as MastodonStatus[]).mockResolvedValueOnce([{id:'10'}] as MastodonStatus[])
    await refresh();await flushPromises()
    expect(w.text()).toContain('有新帖子，点击查看');expect(w.get('[data-feed]').text()).toBe('10');w.unmount()
  })
})
