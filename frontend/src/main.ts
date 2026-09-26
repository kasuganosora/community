import { createApp } from 'vue'
import { pinia } from '@/shared/stores'
import router from '@/shared/router'
import App from './App.vue'
import { setAuthExpiredCallback } from '@/shared/api/client'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'

// TDesign's base token sheet (radii, sizes, light/dark palettes) must load
// before our theme bridge, which remaps those tokens onto the design system.
import 'tdesign-vue-next/es/style/index.css'
import '@/shared/styles/global.css'

const app = createApp(App)

// Global error handler — catches errors from component render, lifecycle hooks,
// event handlers, and setup functions that would otherwise fail silently.
app.config.errorHandler = (err, _instance, info) => {
  console.error(`[Vue runtime error] ${info}:`, err)
}

// Catch unhandled promise rejections from fire-and-forget async calls
// (e.g. event handlers that aren't awaited) that Vue's errorHandler misses.
window.addEventListener('unhandledrejection', (event) => {
  console.error('[Unhandled promise rejection]:', event.reason)
})

app.use(pinia)
app.use(router)

// Register auth expiry callback BEFORE mount so it is active when
// App.vue's onMounted fires initial API calls that could return 401
setAuthExpiredCallback(() => {
  const authStore = useAuthStore()
  authStore.logout()
  router.push({ name: 'login' }).catch((err) => {
    console.warn('Auth expired: failed to navigate to login page', err)
  })
})

app.mount('#app')
