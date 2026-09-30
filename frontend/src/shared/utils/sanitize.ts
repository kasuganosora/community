import DOMPurify from 'dompurify'

let hookRegistered = false

function ensureHookRegistered() {
  if (hookRegistered) return
  hookRegistered = true

  // Force safe attributes on all <a> tags after sanitization
  DOMPurify.addHook('afterSanitizeAttributes', (node) => {
    if (node.tagName === 'A') {
      node.setAttribute('target', '_blank')
      node.setAttribute('rel', 'noopener noreferrer nofollow')
    }
  })
}

export function sanitizeHtml(html: string): string {
  ensureHookRegistered()
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS: ['a', 'br', 'p', 'span', 'strong', 'em', 'del', 'code', 'pre', 'blockquote', 'ul', 'ol', 'li'],
    ALLOWED_ATTR: ['href', 'rel', 'target', 'class'],
    ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):|[^a-z]|[a-z+.-]+(?:[^a-z+.-:]|$))/i,
  })
}

/**
 * Strip HTML tags from a string, returning plain text.
 * Uses sanitizeHtml first to ensure safe parsing.
 */
const BLOCK_TAGS = new Set(['P', 'DIV', 'LI', 'TR', 'BLOCKQUOTE', 'PRE', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6'])

function nodeToText(node: Node): string {
  if (node.nodeType === Node.TEXT_NODE) return node.textContent ?? ''
  if (node.nodeType !== Node.ELEMENT_NODE) return ''

  const el = node as HTMLElement
  if (el.tagName === 'BR') return '\n'

  let out = ''
  el.childNodes.forEach(child => {
    out += nodeToText(child)
  })
  if (BLOCK_TAGS.has(el.tagName) && !out.endsWith('\n')) out += '\n'
  return out
}

export function stripHtml(html: string): string {
  const sanitized = sanitizeHtml(html)
  const tmp = document.createElement('div')
  tmp.innerHTML = sanitized
  return nodeToText(tmp).replace(/[ \t]+\n/g, '\n').replace(/\n{3,}/g, '\n\n').trim()
}
