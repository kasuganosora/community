import { applyInstanceDefaultLocale } from '@/shared/i18n'
import { readonly, ref } from 'vue'
import client from '@/shared/api/client'
import { APP_NAME } from '@/shared/utils/constants'
import { setBrandThemeColor } from './useTheme'

/**
 * Instance branding (Misskey's admin「品牌」meta), served as the `branding`
 * extension of GET /api/v2/instance. Loaded once per page and applied
 * globally: theme colour, and the info / server-error illustrations used by
 * every `.empty-state` through CSS custom properties. The same response's
 * `title` is the site name used in the browser tab.
 */
export interface Branding {
  entrancePageStyle: 'classic' | 'simple'
  showTimelineForVisitor: boolean
  showActivitiesForVisitor: boolean
  iconUrl: string
  app192IconUrl: string
  app512IconUrl: string
  bannerUrl: string
  backgroundImageUrl: string
  notFoundImageUrl: string
  infoImageUrl: string
  serverErrorImageUrl: string
  themeColor: string
  defaultLightTheme: string
  defaultDarkTheme: string
  repositoryUrl: string
  feedbackUrl: string
}

export const DEFAULT_BRANDING: Branding = {
  entrancePageStyle: 'classic',
  showTimelineForVisitor: true,
  showActivitiesForVisitor: true,
  iconUrl: '',
  app192IconUrl: '',
  app512IconUrl: '',
  bannerUrl: '',
  backgroundImageUrl: '',
  notFoundImageUrl: '',
  infoImageUrl: '',
  serverErrorImageUrl: '',
  themeColor: '',
  defaultLightTheme: '',
  defaultDarkTheme: '',
  repositoryUrl: '',
  feedbackUrl: '',
}

const branding = ref<Branding>({ ...DEFAULT_BRANDING })

/** Title already written into the SPA shell by the server, before /api/v2/instance returns. */
function titleFromShell(): string {
  if (typeof document === 'undefined') return ''
  const meta = document.querySelector('meta[name="application-name"]')?.getAttribute('content') ?? ''
  const title = meta.trim()
  return title && title !== APP_NAME ? title : ''
}

const instanceTitleSource = ref(titleFromShell())
/** Site name from the shell or GET /api/v2/instance. Empty until one of those is known. */
export const instanceTitle = readonly(instanceTitleSource)
const loaded = ref(false)
let pending: Promise<Branding> | null = null

/** CSS url() value for an admin-provided image URL. */
export function cssImage(url: string): string {
  return `url(${JSON.stringify(url)})`
}

export function applyBranding(b: Branding) {
  if (typeof document === 'undefined') return
  const root = document.documentElement
  const images: Array<[string, string]> = [['info', b.infoImageUrl], ['error', b.serverErrorImageUrl]]
  for (const [name, url] of images) {
    if (url) {
      root.style.setProperty(`--brand-${name}-image`, cssImage(url))
      root.setAttribute(`data-brand-${name}-image`, '')
    } else {
      root.style.removeProperty(`--brand-${name}-image`)
      root.removeAttribute(`data-brand-${name}-image`)
    }
  }
  setBrandThemeColor(b.themeColor)
}

export function loadBranding(force = false): Promise<Branding> {
  if (loaded.value && !force) return Promise.resolve(branding.value)
  if (!pending) {
    pending = client.get<{ branding?: Partial<Branding>; title?: string; default_locale?: string }>('/api/v2/instance')
      .then(({ data }) => {
        applyInstanceDefaultLocale(data?.default_locale)
        instanceTitleSource.value = typeof data?.title === 'string' ? data.title.trim() : ''
        branding.value = { ...DEFAULT_BRANDING, ...(data?.branding ?? {}) }
        loaded.value = true
        applyBranding(branding.value)
        return branding.value
      })
      .catch(() => branding.value)
      .finally(() => { pending = null })
  }
  return pending
}

export function useBranding() {
  void loadBranding()
  return { branding: readonly(branding), instanceTitle, loaded: readonly(loaded), reload: () => loadBranding(true) }
}
