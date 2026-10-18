/** Split a textarea value into entries: one per line, trimmed, blanks skipped, duplicates dropped (first wins). */
export function parseLines(text: string, normalize: (line: string) => string = (line) => line): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of text.split(/\r?\n/)) {
    const value = normalize(raw.trim())
    if (!value || seen.has(value)) continue
    seen.add(value)
    out.push(value)
  }
  return out
}

/** "https://Example.com:443/path" → "example.com". Returns '' when nothing usable is left. */
export function normalizeDomain(input: string): string {
  return input.trim().toLowerCase()
    .replace(/^[a-z][a-z0-9+.-]*:\/\//, '')
    .replace(/^[^@/]*@/, '')
    .replace(/[/?#].*$/, '')
    .replace(/:\d+$/, '')
    .replace(/\.$/, '')
}

/** Same rule as the server: a dotted host with no empty labels and nothing URL-ish left in it. */
export function isValidDomain(domain: string): boolean {
  if (!domain || domain.length > 253 || !domain.includes('.')) return false
  if (/[\s/@?#\\:*,;]/.test(domain)) return false
  return domain.split('.').every((label) => label !== '' && !label.startsWith('-') && !label.endsWith('-'))
}

export interface KeywordDiff {
  add: string[]
  remove: string[]
}

/** Which keywords to add and which existing ones to drop to go from `before` to `after`. */
export function diffKeywords(before: string[], after: string[]): KeywordDiff {
  const old = new Set(before)
  const next = new Set(after)
  return {
    add: after.filter((k) => !old.has(k)),
    remove: before.filter((k) => !next.has(k)),
  }
}
