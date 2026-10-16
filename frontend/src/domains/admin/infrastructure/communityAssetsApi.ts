import client from '@/shared/api/client'
import type { AvatarDecorationPlacement } from '@/shared/types/mastodon'
export interface AvatarDecorationSettings { limit: number; max_limit: number }
export type AvatarDecorationChoice = Omit<AvatarDecorationPlacement, 'url'>
export interface AvatarDecoration { id: string; name: string; image_url: string; enabled: boolean }
export interface Advertisement { id: string; title: string; image_url: string; target_url: string; enabled: boolean; starts_at: string | null; ends_at: string | null }
export interface ManagedFile { id: string; account_id: string; status_id: number | null; name: string; size: number; content_type: string; url: string; remote: boolean; delete_blocked: string }
export const communityAssetsApi = {
 async files(params: Record<string, string | number>) { return (await client.get<{items:ManagedFile[];total:number;bytes:number;page:number;limit:number}>('/api/v1/admin/files',{params})).data },
 async deleteFile(id: string) { await client.delete(`/api/v1/admin/files/${id}`) },
 async list(kind: 'avatar_decorations' | 'advertisements') { return (await client.get<(AvatarDecoration & Advertisement)[]>(`/api/v1/admin/${kind}`)).data },
 async create(kind: string, data: FormData) { return (await client.post(`/api/v1/admin/${kind}`,data,{headers:{'Content-Type':'multipart/form-data'}})).data },
 async update(kind: string,id:string,data:object) { return (await client.patch(`/api/v1/admin/${kind}/${id}`,data)).data },
 async remove(kind:string,id:string) { await client.delete(`/api/v1/admin/${kind}/${id}`) },
 async decorations() { return (await client.get<AvatarDecoration[]>('/api/v1/avatar_decorations')).data },
 async selectDecoration(id:string) { await client.put('/api/v1/accounts/avatar_decoration',{id}) },
 async decorationSettings() { return (await client.get<AvatarDecorationSettings>('/api/v1/avatar_decorations/settings')).data },
 async adminDecorationSettings() { return (await client.get<AvatarDecorationSettings>('/api/v1/admin/avatar_decorations/settings')).data },
 async saveDecorationSettings(limit: number) { return (await client.put<AvatarDecorationSettings>('/api/v1/admin/avatar_decorations/settings',{limit})).data },
 async selectDecorations(decorations: AvatarDecorationChoice[]) { return (await client.put<{avatar_decorations: AvatarDecorationPlacement[]; limit: number}>('/api/v1/accounts/avatar_decorations',{decorations})).data },
 async advertisements() { return (await client.get<Advertisement[]>('/api/v1/advertisements')).data },
}
