import { createApp } from 'vue'
import { pinia } from '@/shared/stores'
import router from '@/shared/router'
import App from './App.vue'

import '@/shared/styles/global.css'

const app = createApp(App)
app.use(pinia)
app.use(router)
app.mount('#app')
