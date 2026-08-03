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
