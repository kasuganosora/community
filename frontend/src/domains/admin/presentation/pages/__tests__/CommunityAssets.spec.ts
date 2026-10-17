import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import BrandAssetsAdminPage from '../BrandAssetsAdminPage.vue'
import FilesAdminPage from '../FilesAdminPage.vue'
import AvatarDecorationPicker from '@/domains/settings/presentation/components/AvatarDecorationPicker.vue'
import AdvertisementCard from '@/shared/components/widgets/AdvertisementCard.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import type { MastodonAccount } from '@/shared/types/mastodon'
const api=vi.hoisted(()=>({list:vi.fn(),create:vi.fn(),update:vi.fn(),remove:vi.fn(),files:vi.fn(),deleteFile:vi.fn(),decorations:vi.fn(),selectDecoration:vi.fn(),selectDecorations:vi.fn(),decorationSettings:vi.fn(),adminDecorationSettings:vi.fn(),saveDecorationSettings:vi.fn(),advertisements:vi.fn()}))
const auth=vi.hoisted(()=>({currentUser:{id:'1',username:'me',acct:'me',display_name:'Me',avatar_decoration_id:'0'},fetchCurrentUser:vi.fn()}))
vi.mock('@/domains/admin/infrastructure/communityAssetsApi',()=>({communityAssetsApi:api}))
vi.mock('@/domains/auth/presentation/stores/authStore',()=>({useAuthStore:()=>auth}))
beforeEach(()=>{vi.clearAllMocks();api.list.mockResolvedValue([]);api.files.mockResolvedValue({items:[],total:0,bytes:0});api.decorations.mockResolvedValue([]);api.advertisements.mockResolvedValue([]);api.decorationSettings.mockResolvedValue({limit:1,max_limit:16});api.adminDecorationSettings.mockResolvedValue({limit:1,max_limit:16});api.selectDecorations.mockResolvedValue({avatar_decorations:[],limit:1})})
afterEach(()=>{vi.useRealTimers()})
describe('Community assets',()=>{
 it('uploads a selected decoration image and metadata',async()=>{const w=mount(BrandAssetsAdminPage,{props:{kind:'avatar_decorations'}});await flushPromises();await w.find('input[required]').setValue('星光');const input=w.find('input[type="file"]');Object.defineProperty(input.element,'files',{value:[new File(['png'],'frame.png',{type:'image/png'})]});await input.trigger('change');await w.find('form').trigger('submit');await flushPromises();expect(api.create).toHaveBeenCalledOnce();const data=api.create.mock.calls[0]![1] as FormData;expect(data.get('name')).toBe('星光');expect(data.get('file')).toBeInstanceOf(File);w.unmount()})
 it('shows real deletion blockers and never offers deletion for referenced files',async()=>{api.files.mockResolvedValue({total:1,bytes:25,items:[{id:'1',name:'private.png',url:'/media/1/private.png',size:25,account_id:'2',content_type:'image/png',delete_blocked:'仍被私信引用'}]});const w=mount(FilesAdminPage);await flushPromises();expect(w.text()).toContain('仍被私信引用');const button=w.findAll('button').find(b=>b.text()==='永久删除');expect(button?.attributes('disabled')).toBeDefined();expect(api.deleteFile).not.toHaveBeenCalled();w.unmount()})
 it('persists the chosen decoration and refreshes the account',async()=>{api.decorations.mockResolvedValue([{id:'5',name:'星光',image_url:'/emoji-assets/a.png'}]);const w=mount(AvatarDecorationPicker);await flushPromises();await w.find('.decoration-option').trigger('click');await w.find('form').trigger('submit');await flushPromises();expect(api.selectDecorations).toHaveBeenCalledWith([{id:'5',angle:0,flip_h:false,offset_x:0,offset_y:0}]);expect(auth.fetchCurrentUser).toHaveBeenCalledOnce();w.unmount()})
 it('overlays only a locally validated decoration route without replacing the avatar',()=>{const account={avatar:'/media/3/avatar.png',username:'alice',avatar_decoration_url:'/api/v1/avatar_decorations/5/image'} as MastodonAccount;const w=mount(UserAvatar,{props:{account},global:{}});expect(w.find('.user-avatar__image').attributes('src')).toBe('/media/3/avatar.png?size=48');expect(w.find('.avatar-decoration').attributes('src')).toBe('/api/v1/avatar_decorations/5/image');w.unmount();const bad=mount(UserAvatar,{props:{account:{...account,avatar_decoration_url:'https://tracker.test/pixel'}},global:{stubs:{'t-avatar':true}}});expect(bad.find('.avatar-decoration').exists()).toBe(false);bad.unmount()})
 it('labels real ads, rejects script links and stops display after server withdrawal',async()=>{vi.useFakeTimers();api.advertisements.mockResolvedValue([{id:'1',title:'Sponsor',image_url:'/emoji-assets/safe.png',target_url:'https://example.test'},{id:'2',title:'unsafe',image_url:'/emoji-assets/safe.png',target_url:'javascript:alert(1)'}]);localStorage.clear();const w=mount(AdvertisementCard);await vi.advanceTimersByTimeAsync(4100);await flushPromises();expect(w.text()).toContain('广告');expect(w.findAll('a')).toHaveLength(1);expect(w.find('a').attributes('rel')).toContain('noreferrer');expect(w.find('img').attributes('src')).toBe('/emoji-assets/safe.png');api.advertisements.mockResolvedValue([]);await vi.advanceTimersByTimeAsync(5*60000);await flushPromises();expect(w.find('aside').exists()).toBe(false);w.unmount()})
})

it('shows asset loading and retries failure without claiming there are no assets', async () => {
 let reject!: (reason: Error) => void
 api.list.mockImplementationOnce(() => new Promise((_, fail) => { reject = fail })).mockResolvedValueOnce([])
 const w = mount(BrandAssetsAdminPage, { props: { kind: 'advertisements' } })
 expect(w.get('[role=status]').text()).toContain('正在加载')
 expect(w.text()).not.toContain('暂无广告')
 reject(new Error('offline')); await flushPromises()
 expect(w.text()).not.toContain('暂无广告')
 await w.get('[role=alert] button').trigger('click'); await flushPromises()
 expect(w.text()).toContain('暂无广告'); w.unmount()
})

it('ignores a previous asset category response after switching between decorations and ads', async () => {
 let resolve!: (items: unknown[]) => void
 api.list.mockImplementationOnce(() => new Promise(done => { resolve = done })).mockResolvedValueOnce([{ id: 'ad', title: 'Current advertisement', enabled: true, image_url: '/ad.png' }])
 const w = mount(BrandAssetsAdminPage, { props: { kind: 'avatar_decorations' } })
 await w.setProps({ kind: 'advertisements' }); await flushPromises()
 resolve([{ id: 'old', title: 'Previous category', name: 'Previous decoration', enabled: true, image_url: '/old.png' }]); await flushPromises()
 expect(api.list).toHaveBeenLastCalledWith('advertisements')
 expect(w.text()).toContain('Current advertisement')
 expect(w.text()).not.toContain('Previous'); w.unmount()
})

it('keeps decoration choices loading until ready and prevents repeat save submissions', async () => {
 let resolve!: (items: unknown[]) => void
 api.decorations.mockImplementationOnce(() => new Promise(done => { resolve = done }))
 let saved!: () => void
 api.selectDecorations.mockImplementationOnce(() => new Promise(done => { saved = () => done({ avatar_decorations: [], limit: 1 }) }))
 const w = mount(AvatarDecorationPicker)
 expect(w.get('[role=status]').text()).toContain('正在加载挂件')
 expect(w.find('.decoration-option').exists()).toBe(false)
 expect(w.find('button[type=submit]').exists()).toBe(false)
 await w.get('form').trigger('submit')
 expect(api.selectDecorations).not.toHaveBeenCalled()
 resolve([{ id: '5', name: 'Available decoration', image_url: '/decoration.png' }]); await flushPromises()
 await w.get('.decoration-option').trigger('click')
 await w.get('form').trigger('submit'); await w.get('form').trigger('submit')
 expect(api.selectDecorations).toHaveBeenCalledTimes(1)
 expect(w.get('button[type=submit]').text()).toBe('保存中…')
 saved(); await flushPromises(); w.unmount()
})

it('edits geometry and respects the configured limit', async () => {
 api.decorationSettings.mockResolvedValue({ limit: 2, max_limit: 16 })
 api.decorations.mockResolvedValue([{ id: '5', name: 'A', image_url: '/a.png' }, { id: '6', name: 'B', image_url: '/b.png' }, { id: '7', name: 'C', image_url: '/c.png' }])
 const w = mount(AvatarDecorationPicker); await flushPromises()
 const options = w.findAll('.decoration-option')
 await options[0]!.trigger('click'); await options[1]!.trigger('click')
 expect(w.findAll('.decoration-option')[2]!.attributes('disabled')).toBeDefined()
 const angle = w.get('input[aria-label="旋转角度（度）"]'); await angle.setValue('90'); await angle.trigger('change')
 await w.get('input[type=checkbox]').setValue(true)
 const x = w.get('input[aria-label="水平偏移（%）"]'); await x.setValue('99'); await x.trigger('change')
 expect(w.findAll('.decoration-picker__preview .avatar-decoration').length).toBe(2)
 await w.get('form').trigger('submit'); await flushPromises()
 expect(api.selectDecorations).toHaveBeenCalledWith([{ id: '5', angle: 0, flip_h: false, offset_x: 0, offset_y: 0 }, { id: '6', angle: 0.25, flip_h: true, offset_x: 0.25, offset_y: 0 }])
 w.unmount()
})
