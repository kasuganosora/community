import { afterEach, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ExplorePage from '../ExplorePage.vue'
import { tagApi, type TagInfo } from '../../../infrastructure/tagApi'
import { timelineApi } from '../../../infrastructure/timelineApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { invalidateSessionData } from '@/shared/api/sessionScope'
import type { MastodonAccount } from '@/shared/types/mastodon'
vi.mock('vue-router', () => ({ useRoute:()=>({query:{}}), useRouter:()=>({replace:vi.fn()}),onBeforeRouteLeave:vi.fn() }))
vi.mock('@/shared/composables/useFeedStream', () => ({ useFeedStream: vi.fn() }))
vi.mock('@/domains/post/presentation/composables/useStatusActions', () => ({ useStatusActions: () => ({}), applyStatusUpdate: vi.fn() }))
afterEach(()=>vi.restoreAllMocks())
it('clears followed topics on account change and ignores the old account response', async()=>{
 setActivePinia(createPinia());const auth=useAuthStore();auth.currentUser={id:'alice'} as MastodonAccount
 let resolve!: (value:TagInfo[])=>void
 vi.spyOn(tagApi,'followed').mockImplementationOnce(()=>new Promise(r=>{resolve=r})).mockResolvedValueOnce([{name:'bob-topic'}])
 vi.spyOn(timelineApi,'getPublicTimeline').mockResolvedValue([])
 const w=shallowMount(ExplorePage,{global:{stubs:{'router-link':{template:'<a><slot /></a>'}}}});await flushPromises()
 auth.currentUser={id:'bob'} as MastodonAccount;invalidateSessionData();await flushPromises()
 expect(w.text()).toContain('bob-topic');resolve([{name:'alice-private-topic'}]);await flushPromises()
 expect(w.text()).toContain('bob-topic');expect(w.text()).not.toContain('alice-private-topic')
 auth.currentUser=null;invalidateSessionData();await flushPromises();expect(w.text()).not.toContain('bob-topic');w.unmount()
})
