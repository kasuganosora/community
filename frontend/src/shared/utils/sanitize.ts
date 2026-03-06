import DOMPurify from 'dompurify'

export function sanitizeHtml(html: string): string {
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS: ['a', 'br', 'p', 'span', 'strong', 'em', 'del', 'code', 'pre', 'blockquote', 'ul', 'ol', 'li'],
    ALLOWED_ATTR: ['href', 'rel', 'target', 'class'],
    ADD_ATTR: ['target'],
  })
}
