import { createApp } from 'vue'
import SetupApp from './SetupApp.vue'
import { i18n, initialLocale } from './i18n'

document.documentElement.lang = initialLocale()
createApp(SetupApp).use(i18n).mount('#setup')
