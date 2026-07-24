import { ref, shallowRef, computed, watch, onUnmounted } from 'vue'
import type { AxiosRequestConfig } from 'axios'
import client from '@/shared/api/client'

// Unified cache storage — shared across all useSWR and useCachedRequest callers
interface CacheEntry<T> {
  data: T
  timestamp: number
  expiresAt: number
  key: string
}

const cache = new Map<string, CacheEntry<unknown>>()
const pendingRequests = new Map<string, Promise<unknown>>()

// Periodic cache cleanup — removes expired entries every 2 minutes
if (typeof window !== 'undefined') {
  setInterval(() => {
    const now = Date.now()
    for (const [k, v] of cache) {
      if (now >= v.expiresAt) {
        cache.delete(k)
      }
    }
  }, 2 * 60 * 1000)
}

// Default cache time: 5 minutes
const DEFAULT_CACHE_TIME = 5 * 60 * 1000

interface UseSWROptions<T> {
  key: string | (() => string)
  fetcher?: (key: string) => Promise<T>
  config?: AxiosRequestConfig
  refreshInterval?: number
  dedupingInterval?: number
  revalidateOnFocus?: boolean
  revalidateOnReconnect?: boolean
  initialData?: T
  cacheTime?: number
}

export function useSWR<T>(options: UseSWROptions<T>) {
  const {
    key: keySource,
    fetcher: customFetcher,
    config,
    refreshInterval,
    revalidateOnFocus = true,
    revalidateOnReconnect = true,
    initialData,
    cacheTime = DEFAULT_CACHE_TIME,
  } = options

  // Generate cache key
  const cacheKey = computed(() => {
    if (typeof keySource === 'function') {
      return keySource()
    }
    return keySource
  })

  // State
  const data = shallowRef<T | undefined>(initialData)
  const error = ref<Error | null>(null)
  const isLoading = ref(!initialData)
  const isValidating = ref(false)
  let refreshTimer: ReturnType<typeof setInterval> | null = null

  // Default fetcher using axios
  async function defaultFetcher(key: string): Promise<T> {
    // Check cache first
    const cached = cache.get(key)
    const now = Date.now()

    if (cached && now < cached.expiresAt) {
      return cached.data as T
    }

    // Check for pending request (deduplication)
    const pending = pendingRequests.get(key)
    if (pending) {
      return pending as Promise<T>
    }

    // Make new request
    const request = client.get<T>(key, config)
      .then((res) => res.data)
      .then((data) => {
        cache.set(key, {
          data,
          timestamp: Date.now(),
          expiresAt: Date.now() + cacheTime,
          key,
        })
        pendingRequests.delete(key)
        return data
      })
      .catch((err) => {
        pendingRequests.delete(key)
        throw err
      })

    pendingRequests.set(key, request)
    return request
  }

  const fetcher = customFetcher || defaultFetcher

  // Race condition prevention — only the latest request may write data
  let requestVersion = 0

  // Execute request
  async function executeRequest(silent = false) {
    const currentVersion = ++requestVersion
    if (!silent) {
      isLoading.value = true
    }
    isValidating.value = true
    error.value = null

    try {
      const key = cacheKey.value
      if (!key) return

      const result = await fetcher(key)
      // Discard stale response — a newer request has been initiated
      if (currentVersion !== requestVersion) return
      data.value = result
    } catch (err) {
      if (currentVersion !== requestVersion) return
      error.value = err instanceof Error ? err : new Error(String(err))
      if (!silent) {
        throw err
      }
    } finally {
      if (currentVersion === requestVersion) {
        isLoading.value = false
        isValidating.value = false
      }
    }
  }

  // Manual revalidation
  async function mutate(newData?: T, shouldRevalidate = true) {
    if (newData !== undefined) {
      data.value = newData
      cache.set(cacheKey.value, {
        data: newData,
        timestamp: Date.now(),
        expiresAt: Date.now() + cacheTime,
        key: cacheKey.value,
      })
    }

    if (shouldRevalidate) {
      await executeRequest(true)
    }
  }

  // Cleanup function to remove event listeners
  let onFocus: (() => void) | null = null
  let onOnline: (() => void) | null = null

  // Watch for key changes
  const stopKeyWatch = watch(
    cacheKey,
    (newKey, oldKey) => {
      if (newKey && newKey !== oldKey) {
        const cached = cache.get(newKey)
        if (cached && Date.now() < cached.expiresAt) {
          data.value = cached.data as T
          isLoading.value = false
          executeRequest(true)
        } else {
          executeRequest()
        }
      }
    },
    { immediate: true }
  )

  // Refresh interval
  if (refreshInterval && refreshInterval > 0) {
    const stopRefreshWatch = watch(
      () => cacheKey.value,
      (key) => {
        if (refreshTimer) {
          clearInterval(refreshTimer)
        }
        if (key) {
          refreshTimer = setInterval(() => {
            executeRequest(true)
          }, refreshInterval)
        }
      },
      { immediate: true }
    )
    // Store original stop for cleanup
    const originalStop = stopRefreshWatch
    onUnmounted(() => {
      originalStop()
      if (refreshTimer) {
        clearInterval(refreshTimer)
      }
    })
  }

  // Revalidate on window focus
  if (revalidateOnFocus) {
    onFocus = () => {
      const cached = cache.get(cacheKey.value)
      if (!cached || Date.now() > cached.timestamp + cacheTime / 2) {
        executeRequest(true)
      }
    }

    const stopFocusWatch = watch(
      () => cacheKey.value,
      (key, oldKey) => {
        if (oldKey && onFocus) {
          window.removeEventListener('focus', onFocus)
        }
        if (key && onFocus) {
          window.addEventListener('focus', onFocus)
        }
      },
      { immediate: true }
    )
    onUnmounted(() => {
      stopFocusWatch()
      if (onFocus) {
        window.removeEventListener('focus', onFocus)
      }
    })
  }

  // Revalidate on reconnect
  if (revalidateOnReconnect) {
    onOnline = () => {
      executeRequest(true)
    }

    const stopOnlineWatch = watch(
      () => cacheKey.value,
      (key, oldKey) => {
        if (oldKey && onOnline) {
          window.removeEventListener('online', onOnline)
        }
        if (key && onOnline) {
          window.addEventListener('online', onOnline)
        }
      },
      { immediate: true }
    )
    onUnmounted(() => {
      stopOnlineWatch()
      if (onOnline) {
        window.removeEventListener('online', onOnline)
      }
    })
  }

  // Cleanup key watch on unmount
  onUnmounted(() => {
    stopKeyWatch()
  })

  return {
    data: computed(() => data.value),
    error: computed(() => error.value),
    isLoading: computed(() => isLoading.value),
    isValidating: computed(() => isValidating.value),
    mutate,
    refresh: () => executeRequest(true),
  }
}

// Helper to prefetch data
export function prefetchSWR<T>(key: string, fetcher?: (key: string) => Promise<T>) {
  if (cache.has(key)) {
    const entry = cache.get(key)!
    if (Date.now() < entry.expiresAt) {
      return Promise.resolve(entry.data as T)
    }
  }

  const defaultFetcher = async (k: string) => {
    const { data } = await client.get<T>(k)
    return data
  }

  const f = fetcher || defaultFetcher

  return f(key).then((data) => {
    cache.set(key, {
      data,
      timestamp: Date.now(),
      expiresAt: Date.now() + DEFAULT_CACHE_TIME,
      key,
    })
    return data
  })
}

// Helper to clear cache
export function clearSWRCache(key?: string) {
  if (key) {
    cache.delete(key)
  } else {
    cache.clear()
  }
}

// Helper to get cache size
export function getSWRCacheSize() {
  return cache.size
}
