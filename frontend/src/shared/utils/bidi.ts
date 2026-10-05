const LRM = '\u200e'
const NUMBER = /\d+(?:[:./-]\d+)*/g

/** Keep a European number from being pulled into a preceding Arabic run. */
export function markBidiNumbers(text: string): string {
  return text.replace(NUMBER, (token, offset) => {
    const prev = offset > 0 ? text[offset - 1] : ''
    return prev === LRM ? token : LRM + token
  })
}

/** Drop display-only marks before the text is posted or counted. */
export function stripBidiMarks(text: string): string {
  return text.replace(/\u200e/g, '')
}

/**
 * Same mark, for sanitized HTML. Link and code text is left alone so a copied
 * URL does not pick up an invisible character.
 */
export function markBidiNumbersHtml(html: string): string {
  if (!html || typeof document === 'undefined' || !/\d/.test(html)) return html
  const tpl = document.createElement('template')
  tpl.innerHTML = html
  const walker = document.createTreeWalker(tpl.content, NodeFilter.SHOW_TEXT)
  const nodes: Text[] = []
  while (walker.nextNode()) nodes.push(walker.currentNode as Text)
  let changed = false
  for (const node of nodes) {
    if (node.parentElement?.closest('a, code, pre')) continue
    const next = markBidiNumbers(node.data)
    if (next === node.data) continue
    node.data = next
    changed = true
  }
  return changed ? tpl.innerHTML : html
}
