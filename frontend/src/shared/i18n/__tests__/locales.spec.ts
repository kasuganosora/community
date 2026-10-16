import { describe, expect, it } from 'vitest'
import { LOCALES, matchLocale } from '@/shared/i18n'

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
  }
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
