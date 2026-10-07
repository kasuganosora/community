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
