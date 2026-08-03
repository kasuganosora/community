import { createApp } from 'vue'
import { pinia } from '@/shared/stores'
import router from '@/shared/router'
import App from './App.vue'
import { setAuthExpiredCallback } from '@/shared/api/client'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'

import '@/shared/styles/global.css'

const app = createApp(App)
app.use(pinia)
app.use(router)
app.mount('#app')

// Register auth expiry callback — clears token and redirects via SPA router
// instead of hard window.location redirect
setAuthExpiredCallback(() => {
  const authStore = useAuthStore()
  authStore.logout()
  router.push({ name: 'login' })
})
