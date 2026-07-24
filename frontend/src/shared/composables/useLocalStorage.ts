import { ref, watch, onUnmounted, type Ref } from 'vue'

interface UseLocalStorageOptions<T> {
  serializer?: {
    read: (value: string) => T
    write: (value: T) => string
  }
  syncAcrossTabs?: boolean
  defaultValue?: T
}

export function useLocalStorage<T>(
  key: string,
  initialValue: T,
  options: UseLocalStorageOptions<T> = {}
): Ref<T> {
  const {
    serializer = {
      read: (value: string) => JSON.parse(value) as T,
      write: (value: T) => JSON.stringify(value),
    },
    syncAcrossTabs = true,
    defaultValue = initialValue,
  } = options

  // Read from localStorage or use initial value
  function read(): T {
    try {
      const item = localStorage.getItem(key)
      if (item === null) return initialValue
      return serializer.read(item)
    } catch (e) {
      console.error(`Error reading localStorage key "${key}":`, e)
      return defaultValue
    }
  }

  // Write to localStorage
  function write(value: T) {
    try {
      localStorage.setItem(key, serializer.write(value))
    } catch (e) {
      console.error(`Error writing localStorage key "${key}":`, e)
    }
  }

  const storedValue = ref<T>(read()) as Ref<T>

  // Watch for changes and update localStorage
  watch(
    storedValue,
    (newValue) => {
      write(newValue)
    },
    { deep: true }
  )

  // Sync across tabs
  if (syncAcrossTabs && typeof window !== 'undefined') {
    const handleStorage = (e: StorageEvent) => {
      if (e.key === key && e.newValue !== null) {
        try {
          storedValue.value = serializer.read(e.newValue)
        } catch (err) {
          console.error(`Error syncing localStorage key "${key}":`, err)
        }
      }
    }

    window.addEventListener('storage', handleStorage)

    // Clean up event listener on component unmount
    onUnmounted(() => {
      window.removeEventListener('storage', handleStorage)
    })
  }

  return storedValue
}

// Version that returns object with additional utilities
export function useLocalStorageExtended<T>(
  key: string,
  initialValue: T,
  options: UseLocalStorageOptions<T> = {}
) {
  const storage = useLocalStorage(key, initialValue, options)

  function remove() {
    localStorage.removeItem(key)
    storage.value = initialValue
  }

  function exists() {
    return localStorage.getItem(key) !== null
  }

  return {
    value: storage,
    remove,
    exists,
  }
}
