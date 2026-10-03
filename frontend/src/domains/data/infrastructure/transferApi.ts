import client from '@/shared/api/client'
export type TransferScope = 'personal' | 'site'
export interface TransferReport {
  can_execute: boolean
  source: { platform: string; version?: string; base_url: string; account_domain?: string }
  counts: Record<string, number>; created: Record<string, number>; skipped: Record<string, number>; archived: Record<string, number>
  issues: { severity: string; code: string; kind?: string; source_id?: string; message: string }[]
}
export interface TransferJob {
  id: string; scope: TransferScope; source: string; filename: string
  state: 'validating' | 'ready' | 'blocked' | 'running' | 'completed' | 'failed'
  revision: string; report: TransferReport | null
  progress: { phase: string; completed: number; total: number }
  created_at: string; updated_at: string; error?: string
}
const base = (scope: TransferScope) => scope === 'site' ? '/api/v1/admin/data/imports' : '/api/v1/data/imports'
export const transferApi = {
  async list(scope: TransferScope, signal?: AbortSignal) { return (await client.get<TransferJob[]>(base(scope), { signal })).data },
  async get(scope: TransferScope, id: string, signal?: AbortSignal) { return (await client.get<TransferJob>(`${base(scope)}/${encodeURIComponent(id)}`, { signal })).data },
  async upload(scope: TransferScope, file: File, source: string, kind: string, signal?: AbortSignal) {
    const data = new FormData(); data.append('file', file); data.append('source', source); data.append('kind', kind)
    return (await client.post<TransferJob>(base(scope), data, { signal, timeout: 300000, headers: { 'Content-Type': 'multipart/form-data' } })).data
  },
  async execute(scope: TransferScope, job: TransferJob, signal?: AbortSignal) { return (await client.post<TransferJob>(`${base(scope)}/${encodeURIComponent(job.id)}/execute`, { revision: job.revision }, { signal })).data },
  async remove(scope: TransferScope, id: string, signal?: AbortSignal) { await client.delete(`${base(scope)}/${encodeURIComponent(id)}`, { signal }) },
  async report(scope: TransferScope, id: string, signal?: AbortSignal) { return (await client.get<TransferReport>(`${base(scope)}/${encodeURIComponent(id)}/report`, { signal })).data },
  async export(signal?: AbortSignal) { return (await client.get<Blob>('/api/v1/data/export', { responseType: 'blob', timeout: 300000, signal })).data },
}
