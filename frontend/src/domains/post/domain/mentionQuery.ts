/** The @mention being typed at the caret, if any. `start` includes the @. */
export function mentionAtCaret(text: string, caret: number): { q: string; start: number; end: number } | null {
  const before = text.slice(0, Math.max(0, caret))
  const match = /(?:^|\s)@([A-Za-z0-9_]+(?:@[A-Za-z0-9.-]*)?)$/.exec(before)
  const q = match?.[1]
  if (!q) return null
  return { q, start: before.length - q.length - 1, end: before.length }
}
