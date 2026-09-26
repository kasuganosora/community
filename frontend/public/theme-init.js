// Pre-paint appearance: runs synchronously in <head> so the first frame is
// already in the right theme (no light flash for dark-mode users). Mirrors
// resolve() in src/shared/composables/useTheme.ts.
(function () {
  var pref = 'system'
  try { pref = localStorage.getItem('theme-preference') || 'system' } catch (e) { /* ignore */ }
  var dark = pref === 'dark' || (pref !== 'light' && window.matchMedia &&
    window.matchMedia('(prefers-color-scheme: dark)').matches)
  var root = document.documentElement
  root.setAttribute('theme-mode', dark ? 'dark' : 'light')
  root.style.colorScheme = dark ? 'dark' : 'light'
})()
