import { describe, expect, it } from 'vitest'
import { DEFAULT_LOCALE, FALLBACK_LOCALES, LOCALES, localeFromLanguages, matchLocale } from '@/shared/i18n'
import { API_ERROR_KEYS } from '@/shared/i18n/apiErrors'

const files = import.meta.glob<{ default: Record<string, unknown> }>('@/locales/*.json', { eager: true })
const byLocale = Object.fromEntries(Object.entries(files).map(([p, m]) => [p.replace(/^.*\/(.+)\.json$/, '$1'), m.default]))

function flatten(obj: Record<string, unknown>, prefix = ''): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (typeof v === 'string') out[key] = v
    else Object.assign(out, flatten(v as Record<string, unknown>, key))
  }
  return out
}
const placeholders = (s: string) => [...new Set(s.match(/\{\d+\}|\{'[^']*'\}/g) ?? [])].sort()
// vue-i18n treats these as syntax; a literal one must be written as {'@'} etc.
const unescapedSyntax = (s: string) => s.replace(/\{'[^']*'\}|\{\d+\}/g, '').match(/[{}@|$%]/)

describe('locale files', () => {
  const source = flatten(byLocale['zh-CN'])

  it('exist for every supported language', () => {
    expect(Object.keys(byLocale).sort()).toEqual(LOCALES.map(l => l.code).sort())
  })

  for (const { code } of LOCALES) {
    it(`${code} has exactly the zh-CN key set`, () => {
      expect(Object.keys(flatten(byLocale[code])).sort()).toEqual(Object.keys(source).sort())
    })

    it(`${code} keeps every placeholder of the source`, () => {
      const msgs = flatten(byLocale[code])
      const bad = Object.entries(source).filter(([k, v]) => placeholders(msgs[k] ?? '').join() !== placeholders(v).join() || !msgs[k]?.trim())
      expect(bad.map(([k]) => k)).toEqual([])
    })

    it(`${code} has no unescaped message-syntax characters in apiError.*`, () => {
      const msgs = flatten(byLocale[code])
      const bad = Object.entries(msgs).filter(([k, v]) => k.startsWith('apiError.') && unescapedSyntax(v))
      expect(bad.map(([k]) => k)).toEqual([])
    })
  }

  it('defines every apiError key the backend-message tables use', () => {
    expect(API_ERROR_KEYS.filter(k => !(`apiError.${k}` in source))).toEqual([])
  })

  it('has no apiError key that no backend message maps to', () => {
    const used = new Set(API_ERROR_KEYS)
    expect(Object.keys(source).filter(k => k.startsWith('apiError.') && !used.has(k.slice('apiError.'.length)))).toEqual([])
  })
})

describe('matchLocale', () => {
  it('maps browser tags to supported languages', () => {
    expect(matchLocale('zh-HK')).toBe('zh-TW')
    expect(matchLocale('zh-Hant-TW')).toBe('zh-TW')
    expect(matchLocale('zh')).toBe('zh-CN')
    expect(matchLocale('en-GB')).toBe('en')
    expect(matchLocale('de-AT')).toBe('de')
    expect(matchLocale('pt-BR')).toBeNull()
  })
})

describe('default language', () => {
  it('is English when no browser language is supported', () => {
    expect(DEFAULT_LOCALE).toBe('en')
    expect(localeFromLanguages([])).toBe('en')
    expect(localeFromLanguages(['pt-BR', 'es', undefined])).toBe('en')
  })
  it('uses the first supported browser language', () => {
    expect(localeFromLanguages(['pt-BR', 'ja-JP', 'en'])).toBe('ja')
    expect(localeFromLanguages(['zh-Hant-HK'])).toBe('zh-TW')
  })
  it('falls back to English before the zh-CN source', () => {
    expect(FALLBACK_LOCALES).toEqual(['en', 'zh-CN'])
  })
})
