import client from '@/shared/api/client'

export type UgcVisibility = 'all' | 'local' | 'none'
export interface ModerationSettings {
  enable_registration: boolean
  email_required_for_signup: boolean
  auto_close_registration: boolean
  registration_auto_closed_at?: string
  moderators_last_active_at?: string
  moderator_inactivity_limit_days: number
  ugc_visibility_for_visitor: UgcVisibility
  preserved_usernames: string[]
  sensitive_words: string[]
  prohibited_words: string[]
  prohibited_words_for_name_of_user: string[]
  hidden_tags: string[]
  blocked_hosts: string[]
  silenced_hosts: string[]
  media_silenced_hosts: string[]
  environment_locked: string[]
}
export type ModerationUpdate = Partial<Omit<ModerationSettings, 'environment_locked' | 'moderators_last_active_at' | 'registration_auto_closed_at' | 'moderator_inactivity_limit_days'>>
export type InstanceModerationAction = 'block' | 'unblock' | 'suspend' | 'unsuspend' | 'silence' | 'unsilence' | 'media_silence' | 'media_unsilence'
export interface InstanceModerationState { domain: string; is_blocked: boolean; is_suspended: boolean; is_silenced: boolean; is_media_silenced: boolean }

const base = '/api/v1/admin'
export const moderationSettingsApi = {
  async get() { return (await client.get<ModerationSettings>(`${base}/moderation`)).data },
  async save(update: ModerationUpdate) { return (await client.put<ModerationSettings>(`${base}/moderation`, update)).data },
  async instanceAction(domain: string, action: InstanceModerationAction) {
    return (await client.post<InstanceModerationState>(`${base}/federation/instances/${encodeURIComponent(domain)}/moderation`, { action })).data
  },
}

/** Splits textarea input into entries the way Misskey does (one per line, empty lines dropped). */
export function splitLines(text: string): string[] {
  return text.split('\n').map(l => l.replace(/\r$/, '')).filter(l => l.trim() !== '')
}

/** Returns the first /regex/ line that does not compile, or undefined. */
export function invalidRegexLine(lines: string[]): string | undefined {
  for (const line of lines) {
    const m = line.match(/^\/(.+)\/(.*)$/)
    if (!m) continue
    try { new RegExp(m[1]!, m[2]!.replace(/[gyd]/g, '')) } catch { return line }
  }
  return undefined
}
