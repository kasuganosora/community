import type { PiniaPluginContext, StateTree } from 'pinia'

interface PersistOptions {
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

    // Subscribe to store changes
    store.$subscribe(
      () => {
        persistFn()
      },
      { detached: true }
    )
  }
}

// Helper to create encrypted storage
export function createEncryptedStorage(
  encryptionKey: string,
  storage: Storage = localStorage
): Storage {
  // Simple XOR encryption (not secure for production, use proper encryption library)
  const encrypt = (text: string): string => {
    let result = ''
    for (let i = 0; i < text.length; i++) {
      result += String.fromCharCode(
        text.charCodeAt(i) ^ encryptionKey.charCodeAt(i % encryptionKey.length)
      )
    }
    return btoa(result)
  }

  const decrypt = (text: string): string => {
    try {
      const decoded = atob(text)
      let result = ''
      for (let i = 0; i < decoded.length; i++) {
        result += String.fromCharCode(
          decoded.charCodeAt(i) ^ encryptionKey.charCodeAt(i % encryptionKey.length)
        )
      }
      return result
    } catch {
      return ''
    }
  }

  return {
    getItem: (key: string) => {
      const value = storage.getItem(key)
      return value ? decrypt(value) : null
    },
    setItem: (key: string, value: string) => {
      storage.setItem(key, encrypt(value))
    },
    removeItem: (key: string) => {
      storage.removeItem(key)
    },
    clear: () => {
      storage.clear()
    },
    key: (index: number) => storage.key(index),
    get length() {
      return storage.length
    },
  }
}
