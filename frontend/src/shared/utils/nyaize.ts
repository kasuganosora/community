/**
 * Display-time "speak as cat" (Misskey speakAsCat). Our own implementation
 * of Misskey's rules: な→にゃ / ナ→ニャ / ﾅ→ﾆｬ, English "na"→"nya",
 * "morning"→"mornyan", "everyone"→"everynyan", Korean 나–낳→냐–냫 and
 * sentence-final 다→다냥 / 야→냥. Stored and federated content is never
 * changed; only the rendered copy is.
 */

const KO_FIRST = '나'.charCodeAt(0)
const KO_LAST = '낳'.charCodeAt(0)
const KO_SHIFT = '냐'.charCodeAt(0) - KO_FIRST

function keepCase(match: string, lower: string): string {
  return match === match.toUpperCase() && match !== match.toLowerCase() ? lower.toUpperCase() : lower
}

/** Nyaizes plain prose (no markup awareness). */
export function nyaizeProse(text: string): string {
  let out = ''
  for (const ch of text) {
    const code = ch.charCodeAt(0)
    if (ch === 'な') out += 'にゃ'
    else if (ch === 'ナ') out += 'ニャ'
    else if (ch === 'ﾅ') out += 'ﾆｬ'
    else if (code >= KO_FIRST && code <= KO_LAST) out += String.fromCharCode(code + KO_SHIFT)
    else out += ch
  }
  return out
    .replace(/(n)(a)/gi, (_, n: string, a: string) => n + keepCase(a, 'ya'))
    .replace(/(morn)(ing)/gi, (_, head: string, tail: string) => head + keepCase(tail, 'yan'))
    .replace(/(every)(one)/gi, (_, head: string, tail: string) => head + keepCase(tail, 'nyan'))
    .replace(/다(?=$|[.!? ])/gm, '다냥')
    .replace(/야(?=$|[? ])/gm, '냥')
}

// Pieces of text that must stay verbatim: fenced and inline code, math,
// URLs, mentions, hashtags, custom emoji shortcodes and MFM syntax
// ($[fn.args … ] heads, closing brackets, <plain>…</plain>, <center> etc.).
const PROTECTED = new RegExp([
  '```[\\s\\S]*?```',
  '`[^`\\n]*`',
  '\\\\\\([\\s\\S]*?\\\\\\)',
  '\\\\\\[[\\s\\S]*?\\\\\\]',
  '<plain>[\\s\\S]*?</plain>',
  '</?(?:center|small|i|b|s|plain)>',
  '\\$\\[[\\w.,=+-]+\\s?',
  '\\]',
  '[a-z][a-z0-9+.-]*://[^\\s<>"\'`]+',
  '@[\\w.-]+(?:@[\\w.-]+)?',
  '[#＃][^\\s.,!?\'"#:/[\\]【】()「」（）<>]+',
  ':[\\w+-]+:',
].join('|'), 'gi')

/** Nyaizes text written in Markdown / MFM, leaving protected spans alone. */
export function nyaizeText(text: string): string {
  let out = ''
  let last = 0
  for (const m of text.matchAll(PROTECTED)) {
    out += nyaizeProse(text.slice(last, m.index)) + m[0]
    last = (m.index ?? 0) + m[0].length
  }
  return out + nyaizeProse(text.slice(last))
}

const SKIP = new Set(['A', 'CODE', 'PRE', 'KBD', 'SAMP', 'SCRIPT', 'STYLE', 'MATH', 'TEXTAREA'])

/** Nyaizes the text nodes of sanitized status HTML, skipping links
 * (mentions, hashtags, URLs), code and math elements. */
export function nyaizeHtml(html: string): string {
  if (!html || typeof document === 'undefined') return html
  const tpl = document.createElement('template')
  tpl.innerHTML = html
  const walker = document.createTreeWalker(tpl.content, NodeFilter.SHOW_TEXT)
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    let skip = false
    for (let el = node.parentElement; el; el = el.parentElement) {
      if (SKIP.has(el.tagName) || el.classList.contains('mention') || el.classList.contains('hashtag')) { skip = true; break }
    }
    if (!skip && node.nodeValue) node.nodeValue = nyaizeText(node.nodeValue)
  }
  return tpl.innerHTML
}

/** Whether a status by this account is shown nyaized. */
export function speaksAsCat(account: { is_cat?: boolean; speak_as_cat?: boolean } | undefined | null): boolean {
  return !!account?.is_cat && account.speak_as_cat !== false
}
