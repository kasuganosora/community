import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import DataTransferPage from '../DataTransferPage.vue'
import { transferApi, type TransferJob } from '../../../infrastructure/transferApi'
import { invalidateSessionData } from '@/shared/api/sessionScope'
const makeJob = (state: TransferJob['state'] = 'ready'): TransferJob => ({ id: 'j1', scope: 'personal', source: 'community', filename: 'backup.zip', state, revision: 'r1', created_at: '', updated_at: '', progress: { phase: 'preview', completed: 0, total: 4 }, report: { can_execute: state !== 'blocked', source: { platform: 'community', base_url: 'https://old.test' }, counts: { posts: 4 }, created: {}, skipped: { posts: 2 }, archived: { posts: 1 }, issues: [] } })
const button = (w: ReturnType<typeof mount>, name: string) => w.findAll('button').find(b => b.text() === name)!
beforeEach(() => { vi.spyOn(transferApi, 'list').mockResolvedValue([makeJob()]) })
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers() })
describe('data transfer review and account boundaries', () => {
 it('requires explicit review and executes one revision once despite double click', async () => {
  let complete!: (job: TransferJob) => void
  const execute = vi.spyOn(transferApi, 'execute').mockImplementation(() => new Promise(resolve => { complete = resolve }))
  const w = mount(DataTransferPage); await flushPromises(); await button(w, 'backup.zip · 等待确认').trigger('click')
  expect(button(w, '确认执行导入').attributes('disabled')).toBeDefined()
  await w.get('input[type=checkbox]').setValue(true); await button(w, '确认执行导入').trigger('click'); await button(w, '确认执行导入').trigger('click')
  expect(execute).toHaveBeenCalledTimes(1); expect(execute.mock.calls[0]![1].revision).toBe('r1')
  const done = makeJob('completed'); done.report!.skipped['archive:internal-hash'] = 2; complete(done); await flushPromises(); expect(w.text()).toContain('重复跳过'); expect(w.text()).toContain('仅归档'); expect(w.text()).not.toContain('internal-hash'); expect(w.text()).toContain('保留记录'); expect(w.text()).not.toContain('确认执行导入'); w.unmount()
 })
 it('never executes a blocked preview or renders unrecognized sensitive payload fields', async () => {
  const blocked = { ...makeJob('blocked'), private_key: 'SECRET_PRIVATE_KEY', password_hash: 'SECRET_PASSWORD' }
  vi.mocked(transferApi.list).mockResolvedValue([blocked]); const execute = vi.spyOn(transferApi, 'execute')
  const w = mount(DataTransferPage, { props: { scope: 'site' } }); await flushPromises(); await button(w, 'backup.zip · 预检未通过').trigger('click')
  expect(w.text()).not.toContain('SECRET_'); expect(w.find('input[type=checkbox]').exists()).toBe(false); expect(execute).not.toHaveBeenCalled(); expect(w.text()).not.toContain('导出我的个人备份'); w.unmount()
 })
 it('clears private progress on account change and ignores a late prior-account response', async () => {
  vi.mocked(transferApi.list).mockResolvedValue([makeJob('running')]); let resolve!: (job: TransferJob) => void
  vi.spyOn(transferApi, 'get').mockImplementation(() => new Promise(r => { resolve = r }))
  const w = mount(DataTransferPage); await flushPromises(); await button(w, 'backup.zip · 导入中').trigger('click'); await button(w, '刷新任务状态').trigger('click')
  invalidateSessionData(); await flushPromises(); resolve(makeJob('completed')); await flushPromises()
  expect(w.text()).not.toContain('backup.zip'); expect(w.find('[aria-label="预检与执行报告"]').exists()).toBe(false); w.unmount()
 })
 it('recovers the same task after execute timeout rather than reuploading or retrying automatically', async () => {
  vi.spyOn(transferApi, 'execute').mockRejectedValue(new Error('请求超时')); vi.spyOn(transferApi, 'get').mockResolvedValue(makeJob('running')); const upload = vi.spyOn(transferApi, 'upload')
  const w = mount(DataTransferPage); await flushPromises(); await button(w, 'backup.zip · 等待确认').trigger('click'); await w.get('input[type=checkbox]').setValue(true); await button(w, '确认执行导入').trigger('click'); await flushPromises()
  expect(w.text()).toContain('导入中'); expect(w.text()).toContain('不要重新上传'); expect(upload).not.toHaveBeenCalled(); w.unmount()
 })
 it('retains a personal package validation rejection and offers no site credentials inputs', async () => {
  vi.spyOn(transferApi, 'upload').mockRejectedValue(new Error('个人导入不接受整站包'))
  const w = mount(DataTransferPage); await flushPromises(); const input = w.get('input[type=file]'); Object.defineProperty(input.element, 'files', { value: [new File(['site'], 'site.zip')] }); await input.trigger('change'); await w.get('form').trigger('submit'); await flushPromises()
  expect(w.get('[role=alert]').text()).toContain('不接受整站包'); expect(w.find('input[type=password]').exists()).toBe(false); expect(w.text()).toContain('site.zip'); w.unmount()
 })
})

it('shows pending task reads and a recoverable error without a false empty list', async () => {
 let reject!: (reason: Error) => void
 vi.mocked(transferApi.list).mockImplementationOnce(() => new Promise((_, fail) => { reject = fail })).mockResolvedValueOnce([])
 const w = mount(DataTransferPage)
 expect(w.get('[aria-label="导入任务"]').text()).toContain('正在读取任务')
 expect(w.text()).not.toContain('暂无任务')
 reject(new Error('读取超时')); await flushPromises()
 expect(w.get('[role=alert]').text()).toBe('读取超时'); expect(w.text()).not.toContain('暂无任务')
 await button(w, '刷新任务列表').trigger('click'); await flushPromises()
 expect(w.text()).toContain('暂无任务'); w.unmount()
})

it('marks task refresh pending and discards an older selected task failure', async () => {
 const second = { ...makeJob(), id: 'j2', filename: 'second.zip' }
 vi.mocked(transferApi.list).mockResolvedValue([makeJob(), second])
 let reject!: (reason: Error) => void
 const get = vi.spyOn(transferApi, 'get').mockImplementationOnce(() => new Promise((_, fail) => { reject = fail }))
 const w = mount(DataTransferPage); await flushPromises(); await button(w, 'backup.zip · 等待确认').trigger('click')
 await button(w, '刷新任务状态').trigger('click')
 expect(button(w, '刷新中…').attributes('disabled')).toBeDefined()
 await button(w, '刷新中…').trigger('click'); expect(get).toHaveBeenCalledTimes(1)
 await button(w, 'second.zip · 等待确认').trigger('click'); reject(new Error('older task failed')); await flushPromises()
 expect(w.find('[role=alert]').exists()).toBe(false)
 expect(w.get('[aria-label="预检与执行报告"]').text()).toContain('second.zip'); w.unmount()
})
