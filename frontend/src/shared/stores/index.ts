import { createPinia } from 'pinia'
import { piniaPersistPlugin } from '@/shared/plugins/piniaPersist'

export const pinia = createPinia()

// Use persist plugin
pinia.use(piniaPersistPlugin({
  storage: localStorage,
}))

// Export store types
export type AppPinia = typeof pinia
