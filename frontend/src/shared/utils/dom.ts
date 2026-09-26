const INTERACTIVE = 'a, button, input, textarea, select, label, summary, video, audio, [role="button"], [role="link"], [role="menuitem"], .t-dropdown, .t-dialog'

/**
 * True when a click on a clickable container (e.g. a post card) should be
 * left alone: it hit a nested control/link, or the user is selecting text.
 */
export function isInteractiveClick(e: MouseEvent): boolean {
  const target = e.target as Element | null
  const container = e.currentTarget as Element | null
  const hit = target?.closest?.(INTERACTIVE)
  if (hit && hit !== container && (!container || container.contains(hit))) return true
  // Clicks inside teleported overlays (dialogs, dropdowns) bubble through the
  // component tree but are not DOM descendants of the card.
  if (target && container && !container.contains(target)) return true
  const selection = typeof window !== 'undefined' ? window.getSelection?.() : null
  return !!selection && !selection.isCollapsed && selection.toString().length > 0
}
