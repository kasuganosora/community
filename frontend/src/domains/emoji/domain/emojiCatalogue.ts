import type { MastodonEmoji } from '@/shared/types/mastodon'

/** Persisted catalogue. Bump the suffix when the stored shape changes. */
export const EMOJI_CATALOGUE_STORAGE_KEY = 'community:custom-emoji-catalogue:v1'
export const EMOJI_CATALOGUE_PATH = '/api/v1/custom_emojis'

export interface CachedEmojiCatalogue {
  revision: string | null
  emojis: MastodonEmoji[]
}

export interface CatalogueResult {
  unchanged: boolean
  revision: string | null
  emojis: MastodonEmoji[]
}

interface StoredEmojiCatalogue {
  v: 1
  revision: string | null
  emojis: MastodonEmoji[]
}

export function readCachedEmojiCatalogue(): CachedEmojiCatalogue | null {
  try {
    const raw = localStorage.getItem(EMOJI_CATALOGUE_STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<StoredEmojiCatalogue>
    if (parsed.v !== 1) return null
    const emojis = parseEmojiList(parsed.emojis)
    const revision = typeof parsed.revision === 'string' && parsed.revision ? parsed.revision : null
    return { revision, emojis }
  } catch {
    return null
  }
}

export function writeCachedEmojiCatalogue(entry: CachedEmojiCatalogue): void {
  const payload: StoredEmojiCatalogue = { v: 1, revision: entry.revision, emojis: entry.emojis }
  try {
    localStorage.setItem(EMOJI_CATALOGUE_STORAGE_KEY, JSON.stringify(payload))
  } catch {
    // A full disk or private mode keeps the in-memory copy for this tab.
  }
}

/** Interprets a conditional catalogue response. A 304 keeps the caller's list. */
export function catalogueFromResponse(sent: string | null, status: number, etag: string | null, body: unknown): CatalogueResult {
  if (status === 304) return { unchanged: true, revision: sent, emojis: [] }
  if (status !== 200) throw new Error('emoji catalogue failed')
  return { unchanged: false, revision: etag, emojis: parseEmojiList(body) }
}

function parseEmojiList(body: unknown): MastodonEmoji[] {
  if (!Array.isArray(body)) throw new Error('invalid emoji catalogue')
  return body.map(parseEmoji)
}

function parseEmoji(value: unknown): MastodonEmoji {
  if (!value || typeof value !== 'object') throw new Error('invalid emoji catalogue')
  const row = value as Record<string, unknown>
  if (typeof row.shortcode !== 'string' || row.shortcode.length === 0) throw new Error('invalid emoji catalogue')
  if (typeof row.url !== 'string' || (row.static_url != null && typeof row.static_url !== 'string')) throw new Error('invalid emoji catalogue')
  if (typeof row.visible_in_picker !== 'boolean') throw new Error('invalid emoji catalogue')
  const emoji: MastodonEmoji = {
    shortcode: row.shortcode,
    url: row.url,
    static_url: typeof row.static_url === 'string' ? row.static_url : '',
    visible_in_picker: row.visible_in_picker,
  }
  if (row.category != null) {
    if (typeof row.category !== 'string') throw new Error('invalid emoji catalogue')
    emoji.category = row.category
  }
  if (row.aliases != null) {
    if (!Array.isArray(row.aliases) || row.aliases.some(alias => typeof alias !== 'string')) throw new Error('invalid emoji catalogue')
    emoji.aliases = row.aliases as string[]
  }
  if (row.license != null) {
    if (typeof row.license !== 'string') throw new Error('invalid emoji catalogue')
    emoji.license = row.license
  }
  if (row.local_only != null) {
    if (typeof row.local_only !== 'boolean') throw new Error('invalid emoji catalogue')
    emoji.local_only = row.local_only
  }
  if (row.is_sensitive != null) {
    if (typeof row.is_sensitive !== 'boolean') throw new Error('invalid emoji catalogue')
    emoji.is_sensitive = row.is_sensitive
  }
  if (row.disabled != null) {
    if (typeof row.disabled !== 'boolean') throw new Error('invalid emoji catalogue')
    emoji.disabled = row.disabled
  }
  return emoji
}
