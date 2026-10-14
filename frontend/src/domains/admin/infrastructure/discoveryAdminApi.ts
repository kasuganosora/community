import client from '@/shared/api/client'
import type { TargetKind } from '@/domains/discovery/infrastructure/discoveryApi'

export interface DiscoveryAdminStatus {
  enabled: boolean; trends_enabled: boolean; configured_mode: string; algorithm_version: string; approved_version: string; require_manual_approval?: boolean
  index: { ready: boolean; reason: string; generation: string; version: string; mode: string; document_count: number; data_cutoff: string; checked_at: string; last_build_error?: string; last_build_error_at?: string; last_query_error?: string; last_query_error_at?: string }
  runtime?: {
    enabled: boolean; redis_available: boolean; reason?: string; pool_generation?: string; pool_as_of?: string; pool_mode?: string
    pool_candidates: number; pool_stale: boolean; pool_truncated: boolean; pool_payload_bytes: number; pool_lease_active: boolean
    pool_lease_remaining_ms: number; snapshot_reservations: number; snapshot_maximum: number; snapshot_reserved_bytes: number; cache_budget_bytes: number
  }
}
export interface DiscoverySuppression { id: number | string; action: string; target_kind: TargetKind; target_key: string; reason: string; created_at: string }
export const discoveryAdminApi = {
  async status() { return (await client.get<DiscoveryAdminStatus>('/api/v1/admin/discovery/status')).data },
  async feedback() { return (await client.get<{ items: DiscoverySuppression[] }>('/api/v1/admin/discovery/feedback')).data.items },
  async suppress(value: { target_kind: TargetKind; target_key: string; reason: string }) { await client.post('/api/v1/admin/discovery/feedback', value) },
  async approve(version: string) { return (await client.post<{ approved_version: string }>('/api/v1/admin/discovery/approve', { version })).data },
  async restore(id: number | string) { await client.delete(`/api/v1/admin/discovery/feedback/${encodeURIComponent(id)}`) },
}
