import type { PiniaPluginContext, StateTree } from 'pinia'

export interface PersistOptions {
  key?: string
  storage?: Storage
  paths?: string[]
  beforeRestore?: (context: PiniaPluginContext) => void
  afterRestore?: (context: PiniaPluginContext) => void
  serializer?: {
    serialize: (value: unknown) => string
    deserialize: (value: string) => unknown
  }
}

type StoreWithPersist = PiniaPluginContext['store'] & {
  $persist: () => void
  $restore: () => void
}

export function piniaPersistPlugin(options: PersistOptions = {}) {
  const {
    storage = localStorage,
    serializer = {
      serialize: JSON.stringify,
      deserialize: JSON.parse,
    },
  } = options

  return (context: PiniaPluginContext) => {
    const { store, options: storeOptions } = context
    const persist = (storeOptions as { persist?: PersistOptions | boolean }).persist

    if (!persist) return

    const persistConfig: PersistOptions = typeof persist === 'object' ? persist : {}
    const storageKey = persistConfig.key || store.$id
    const paths = persistConfig.paths

    // Before restore hook
    persistConfig.beforeRestore?.(context)

    // Restore state from storage
    try {
      const stored = storage.getItem(storageKey)
      if (stored) {
        const parsed = serializer.deserialize(stored) as StateTree

        if (paths) {
          // Only restore specified paths
          const partialState: StateTree = {}
          for (const path of paths) {
            if (path in parsed) {
              partialState[path] = parsed[path]
            }
          }
          store.$patch(partialState)
        } else {
          // Restore entire state
          store.$patch(parsed)
        }
      }
    } catch (e) {
      console.error(`Failed to restore store "${store.$id}":`, e)
    }

    // After restore hook
    persistConfig.afterRestore?.(context)

    // Persist function
    const persistFn = () => {
      try {
        let stateToPersist: StateTree
        const state = store.$state

        if (paths) {
          // Only persist specified paths
          stateToPersist = {}
          for (const path of paths) {
            stateToPersist[path] = state[path]
          }
        } else {
          // Persist entire state
          stateToPersist = { ...state }
        }

        storage.setItem(storageKey, serializer.serialize(stateToPersist))
      } catch (e) {
        console.error(`Failed to persist store "${store.$id}":`, e)
      }
    }

    // Restore function
    const restoreFn = () => {
      try {
        const stored = storage.getItem(storageKey)
        if (stored) {
          const parsed = serializer.deserialize(stored) as StateTree
          store.$patch(parsed)
        }
      } catch (e) {
        console.error(`Failed to restore store "${store.$id}":`, e)
      }
    }

    // Add methods to store
    ;(store as StoreWithPersist).$persist = persistFn
    ;(store as StoreWithPersist).$restore = restoreFn

    // Subscribe to store changes — debounced to avoid excessive writes
    let persistTimer: ReturnType<typeof setTimeout> | null = null
    store.$subscribe(
      () => {
        if (persistTimer) clearTimeout(persistTimer)
        persistTimer = setTimeout(() => persistFn(), 300)
      },
      { detached: true }
    )
  }
}
