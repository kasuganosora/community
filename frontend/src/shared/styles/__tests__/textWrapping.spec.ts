import { describe, expect, it } from 'vitest'

// Vitest blanks CSS imports (even ?raw), so the stylesheets are read from
// disk. The app tsconfig has no Node types: type the few calls used here.
type Fs = {
  readFileSync(path: string, encoding: 'utf8'): string
  readdirSync(path: string, options: { withFileTypes: true }): { name: string, isDirectory(): boolean }[]
}
const fs = (await import('node:fs' as string)) as Fs

// word-break: break-all splits English words anywhere ("th / is"). Prose must
// wrap at spaces (overflow-wrap: anywhere still splits a long URL, and CJK
// breaks between characters anyway). Only machine strings may use break-all.
const SRC = 'src/' // Vitest runs from the frontend directory
const BREAK_ALL_ALLOWED = [
  'domains/admin/presentation/pages/JobsAdminPage.vue', // activity ids and inbox URLs
  'domains/settings/presentation/components/TwoFactorSettings.vue', // base32 secret
]

function styleSources(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    if (entry.isDirectory()) return entry.name === '__tests__' ? [] : styleSources(`${dir}${entry.name}/`)
    return /\.(vue|css)$/.test(entry.name) ? [`${dir}${entry.name}`] : []
  })
}

describe('text wrapping', () => {
  it('toasts wrap English at spaces instead of TDesign\'s break-all', () => {
    const overrides = fs.readFileSync(`${SRC}shared/styles/tdesign-overrides.css`, 'utf8')
    // Must out-rank TDesign's ".t-message__list .t-message { word-break: break-all }".
    const rule = overrides.match(/html \.t-message__list \.t-message,\s*html \.t-message \{([^}]*)\}/)
    expect(rule).not.toBeNull()
    expect(rule![1]).toMatch(/word-break:\s*normal/)
    expect(rule![1]).toMatch(/overflow-wrap:\s*anywhere/)
  })

  it('lets a long toast use the screen width, not half of it', () => {
    const overrides = fs.readFileSync(`${SRC}shared/styles/tdesign-overrides.css`, 'utf8')
    const list = overrides.match(/html \.t-message__list \{([^}]*)\}/)
    expect(list).not.toBeNull()
    expect(list![1]).toMatch(/width:\s*max-content/)
    expect(list![1]).toMatch(/max-width:\s*min\([^;]*100vw/)
  })

  it('uses break-all only for machine strings', () => {
    const offenders = styleSources(SRC)
      .filter(path => /word-break:\s*break-all/.test(fs.readFileSync(path, 'utf8')))
      .map(path => path.slice(SRC.length))
    expect(offenders.sort()).toEqual(BREAK_ALL_ALLOWED)
  })
})
