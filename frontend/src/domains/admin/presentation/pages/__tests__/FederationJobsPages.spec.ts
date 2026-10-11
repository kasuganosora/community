import {afterEach,expect,it,vi} from 'vitest'
import {mount,flushPromises} from '@vue/test-utils'
import JobsAdminPage from '../JobsAdminPage.vue'
import InstancesAdminPage from '../InstancesAdminPage.vue'
import WebhooksAdminPage from '../WebhooksAdminPage.vue'
import {federationJobsApi as api} from '../../../infrastructure/federationJobsApi'
afterEach(()=>vi.restoreAllMocks())
it('confirms cancellation and retains job after a conflict',async()=>{
 vi.spyOn(api,'jobs').mockResolvedValue({total:1,items:[{id:'1',kind:'delivery',state:'failed',attempts:5,last_error:'HTTP 503',next_attempt_at:null,created_at:''}]})
 const action=vi.spyOn(api,'jobAction').mockRejectedValue(new Error('conflict'));const confirm=vi.spyOn(window,'confirm').mockReturnValue(false)
 const w=mount(JobsAdminPage);await flushPromises();const cancel=w.findAll('button').find(b=>b.text()==='取消任务')!;await cancel.trigger('click');expect(action).not.toHaveBeenCalled();confirm.mockReturnValue(true);await cancel.trigger('click');await flushPromises();expect(w.get('[role=alert]').text()).toContain('变化');expect(w.text()).toContain('#1');w.unmount()
})
it('shows real instance relationship counts without a fake block action',async()=>{vi.spyOn(api,'instances').mockResolvedValue({total:1,items:[{domain:'remote.example',accounts:3,suspended_accounts:0,following:2,followers:1,last_activity:null}]});const w=mount(InstancesAdminPage);await flushPromises();expect(w.findAll('tbody td').map(x=>x.text())).toEqual(['remote.example','3','0','2','1','尚无记录']);expect(w.findAll('button').some(x=>x.text()==='封禁')).toBe(false);w.unmount()})
it('shows a creation secret once and allows hiding it',async()=>{vi.spyOn(api,'hooks').mockResolvedValue({items:[],total:0});vi.spyOn(api,'deliveries').mockResolvedValue({items:[],total:0});vi.spyOn(api,'createHook').mockResolvedValue({subscription:{id:'1',url:'https://receiver.example/hook',description:'',events:['status.created'],enabled:true},secret:'one-time-signing-secret'});const w=mount(WebhooksAdminPage);await flushPromises();expect(w.text()).not.toContain('one-time-signing-secret');await w.get('input[type=url]').setValue('https://receiver.example/hook');await w.findAll('form')[0]!.trigger('submit');await flushPromises();expect(w.text()).toContain('one-time-signing-secret');await w.findAll('button').find(x=>x.text()==='已保存，隐藏密钥')!.trigger('click');expect(w.text()).not.toContain('one-time-signing-secret');w.unmount()})
it('labels expired feed repair clearly and confirms cancelling the whole dependency group',async()=>{
 vi.spyOn(api,'jobs').mockResolvedValue({total:2,items:[{id:'4',kind:'feed-batch',state:'failed',parent_id:'3',batch_no:2,requires_reconciliation:true,can_retry:true,attempts:11,last_error:'replay_window_expired',next_attempt_at:null,created_at:''},{id:'5',kind:'feed-event',state:'processing',can_retry:false,attempts:1,last_error:'',next_attempt_at:null,created_at:''}]})
 const action=vi.spyOn(api,'jobAction').mockResolvedValue({updated:true,reconciled:true});const confirm=vi.spyOn(window,'confirm').mockReturnValue(true)
 const w=mount(JobsAdminPage);await flushPromises()
 expect(w.findAll('select')[0]!.findAll('option').map(o=>o.attributes('value'))).toContain('feed-batch')
 expect(w.text()).toContain('所属事件：#3');expect(w.findAll('button').filter(b=>b.text()==='取消任务')).toHaveLength(1)
 await w.findAll('button').find(b=>b.text()==='重新协调缓存')!.trigger('click');await flushPromises()
 expect(confirm).toHaveBeenCalledWith(expect.stringContaining('不会重播历史帖子'))
 expect(action).toHaveBeenCalledWith(expect.objectContaining({id:'4',kind:'feed-batch'}),'retry')
 expect(w.get('[role=status]').text()).toContain('当前数据库')
 await w.findAll('button').find(b=>b.text()==='取消任务')!.trigger('click');await flushPromises()
 expect(confirm).toHaveBeenLastCalledWith(expect.stringContaining('全部尚未完成的依赖批次'))
 w.unmount()
})
it('keeps private-message bridge jobs read-only for administrators', async () => {
 vi.spyOn(api, 'jobs').mockResolvedValue({ total: 1, items: [{ id: '$event', kind: 'matrix-direct', state: 'failed', can_retry: true, attempts: 3, last_error: 'bridge_failed', next_attempt_at: null, created_at: '' }] })
 const action = vi.spyOn(api, 'jobAction')
 const w = mount(JobsAdminPage); await flushPromises()
 await w.findAll('select')[0]!.setValue('matrix-direct')
 expect(w.text()).toContain('不代表远端服务器已经收到消息')
 expect(w.findAll('button').some(b => ['立即重试', '取消任务'].includes(b.text()))).toBe(false)
 expect(action).not.toHaveBeenCalled()
 w.unmount()
})
