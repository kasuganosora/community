import { watch } from 'vue'
import { instanceTitle } from '@/shared/composables/useBranding'
import { APP_NAME } from '@/shared/utils/constants'

/** Site name shown in the tab: the configured instance title, or the product name. */
export function appDisplayName(title = instanceTitle.value): string {
  const name = title.trim()
  return name || APP_NAME
}

/** "<page> - <site>", or just the site name when the page has no heading. */
export function formatDocumentTitle(page: string | undefined, appName = appDisplayName()): string {
  const brand = appName.trim() || APP_NAME
  if (!page || page === APP_NAME) return brand
  return `${page} - ${brand}`
}

let activePage: string | undefined

function publishDocumentTitle() {
  if (typeof document === 'undefined') return
  document.title = formatDocumentTitle(activePage)
}

/** Remember the current page heading and write document.title. */
export function setDocumentTitle(page: string | undefined) {
  activePage = page
  publishDocumentTitle()
}

// Instance title arrives after the first navigation. Refresh the open tab,
// including a page that replaced the route heading (a direct message).
watch(() => instanceTitle.value, () => publishDocumentTitle(), { flush: 'sync' })
