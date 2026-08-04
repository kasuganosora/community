import { ref, watch, onUnmounted, nextTick, type Ref } from 'vue'

interface UseLocalStorageOptions<T> {
  serializer?: {
    read: (value: string) => T
    write: (value: T) => string
  }
  syncAcrossTabs?: boolean
  defaultValue?: T
}

interface LocalStorageResult<T> {
  ref: Ref<T>
  suppressWrite: { value: boolean }
  write: (value: T) => void
}

// Internal version that exposes suppress flag
function createLocalStorage<T>(
  key: string,
  initialValue: T,
  options: UseLocalStorageOptions<T> = {}
): LocalStorageResult<T> {
  const {
    serializer = {
      read: (value: string) => JSON.parse(value) as T,
      write: (value: T) => JSON.stringify(value),
    },
    syncAcrossTabs = true,
    defaultValue = initialValue,
  } = options

  const suppressWrite = { value: false }

  function read(): T {
    if (typeof window === 'undefined') return initialValue
    try {
      const item = localStorage.getItem(key)
      if (item === null) return initialValue
      return serializer.read(item)
    } catch (e) {
      console.error(`Error reading localStorage key "${key}":`, e)
      return defaultValue
    }
  }

  function write(value: T) {
    if (typeof window === 'undefined') return
    try {
      localStorage.setItem(key, serializer.write(value))
    } catch (e) {
      console.error(`Error writing localStorage key "${key}":`, e)
    }
  }

  const storedValue = ref<T>(read()) as Ref<T>

  let writeTimer: ReturnType<typeof setTimeout> | null = null
  let pendingValue: T = storedValue.value
  watch(
    storedValue,
    (newValue) => {
      if (suppressWrite.value) return
      pendingValue = newValue
      if (writeTimer) clearTimeout(writeTimer)
      writeTimer = setTimeout(() => {
        write(pendingValue)
        writeTimer = null
      }, 300)
    },
    { deep: true }
  )

  onUnmounted(() => {
    if (writeTimer) {
      clearTimeout(writeTimer)
      writeTimer = null
      // Flush pending write to avoid data loss when component unmounts
      write(pendingValue)
    }
  })

  if (syncAcrossTabs && typeof window !== 'undefined') {
    const handleStorage = (e: StorageEvent) => {
      if (e.key === key && e.newValue !== null) {
        try {
          // Suppress the watch write to prevent infinite cross-tab write loop
          suppressWrite.value = true
          storedValue.value = serializer.read(e.newValue)
          nextTick(() => { suppressWrite.value = false })
        } catch (err) {
          suppressWrite.value = false
          console.error(`Error syncing localStorage key "${key}":`, err)
        }
      }
    }

    window.addEventListener('storage', handleStorage)
    onUnmounted(() => {
      window.removeEventListener('storage', handleStorage)
    })
  }

  return { ref: storedValue, suppressWrite, write }
}

export function useLocalStorage<T>(
  key: string,
  initialValue: T,
  options: UseLocalStorageOptions<T> = {}
): Ref<T> {
  return createLocalStorage(key, initialValue, options).ref
}

// Version that returns object with additional utilities
export function useLocalStorageExtended<T>(
  key: string,
  initialValue: T,
  options: UseLocalStorageOptions<T> = {}
) {
  const { ref: storage, suppressWrite } = createLocalStorage(key, initialValue, options)

  function remove() {
    if (typeof window === 'undefined') return
    try {
      suppressWrite.value = true
      localStorage.removeItem(key)
      storage.value = initialValue
      nextTick(() => { suppressWrite.value = false })
    } catch (e) {
      console.error(`Error removing localStorage key "${key}":`, e)
      suppressWrite.value = false
    }
  }

  function exists() {
    if (typeof window === 'undefined') return false
    try {
      return localStorage.getItem(key) !== null
    } catch {
      return false
    }
  }

  return {
    value: storage,
    remove,
    exists,
  }
}
