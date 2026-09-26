import { ref, readonly } from 'vue'

/**
 * Appearance preference: follow the system, or force light/dark.
 *
 * The resolved mode is written to <html theme-mode="…">, which is both our
 * token switch (theme-dark.css) and TDesign's own dark-mode hook. The same
 * logic runs as a tiny inline script in index.html before first paint, so
 * there is no light flash on load; this module keeps it in sync afterwards
 * (system changes, user choice) and updates the browser chrome colour.
 */
export type ThemePreference = 'system' | 'light' | 'dark'
export type ThemeMode = 'light' | 'dark'

const STORAGE_KEY = 'theme-preference'
const THEME_COLORS: Record<ThemeMode, string> = { light: '#ffffff', dark: '#000000' }

const media = typeof window !== 'undefined' && window.matchMedia
  ? window.matchMedia('(prefers-color-scheme: dark)')
  : null

function readPreference(): ThemePreference {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return v === 'light' || v === 'dark' ? v : 'system'
  } catch {
    return 'system'
  }
}

const preference = ref<ThemePreference>(readPreference())
const mode = ref<ThemeMode>('light')

function resolve(pref: ThemePreference): ThemeMode {
  if (pref === 'system') return media?.matches ? 'dark' : 'light'
  return pref
}

function apply(next: ThemeMode) {
  mode.value = next
  const root = document.documentElement
  root.setAttribute('theme-mode', next)
  root.style.colorScheme = next
  // One theme-color meta driven from here once JS is running (the
  // media-scoped pair in index.html covers first paint).
  document.querySelectorAll('meta[name="theme-color"]').forEach((m) => {
    m.setAttribute('content', THEME_COLORS[next])
  })
}

/**
 * Switch with a short cross-fade where the View Transitions API exists;
 * otherwise (and under reduced motion, where even a fade of the whole page
 * is unwelcome) switch instantly.
 */
function applyAnimated(next: ThemeMode) {
  if (next === mode.value) return
  const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  const doc = document as Document & { startViewTransition?: (cb: () => void) => unknown }
  if (!reduce && typeof doc.startViewTransition === 'function') {
    doc.startViewTransition(() => apply(next))
  } else {
    apply(next)
  }
}

let initialized = false

export function initTheme() {
  if (initialized || typeof document === 'undefined') return
  initialized = true
  apply(resolve(preference.value))
  media?.addEventListener('change', () => {
    if (preference.value === 'system') applyAnimated(resolve('system'))
  })
  // Keep several tabs in step.
  window.addEventListener('storage', (e) => {
    if (e.key !== STORAGE_KEY) return
    preference.value = readPreference()
    applyAnimated(resolve(preference.value))
  })
}

export function useTheme() {
  initTheme()

  function setPreference(pref: ThemePreference) {
    preference.value = pref
    try {
      if (pref === 'system') localStorage.removeItem(STORAGE_KEY)
      else localStorage.setItem(STORAGE_KEY, pref)
    } catch {
      /* storage unavailable (private mode): still apply for this session */
    }
    applyAnimated(resolve(pref))
  }

  return {
    preference: readonly(preference),
    mode: readonly(mode),
    setPreference,
  }
}
