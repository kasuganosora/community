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

const MAX_CACHE_SIZE = 500

const cache = new Map<string, CacheEntry<unknown>>()
const pendingRequests = new Map<string, Promise<unknown>>()

function setCacheEntry(key: string, data: unknown, cacheTime: number) {
  // LRU eviction: evict oldest entries if cache is full
  if (cache.size >= MAX_CACHE_SIZE) {
    let oldestKey: string | null = null
    let oldestTime = Infinity
    for (const [k, v] of cache) {
      if (v.timestamp < oldestTime) {
        oldestTime = v.timestamp
        oldestKey = k
      }
    }
    if (oldestKey) cache.delete(oldestKey)
  }
  cache.set(key, {
    data,
    timestamp: Date.now(),
    expiresAt: Date.now() + cacheTime,
    key,
  })
}

// Periodic cache cleanup — lazy-started when first useSWR instance mounts
let cleanupTimer: ReturnType<typeof setInterval> | null = null
let activeInstanceCount = 0

function startCleanupTimer() {
  if (cleanupTimer) return
  cleanupTimer = setInterval(() => {
    const now = Date.now()
    for (const [k, v] of cache) {
      if (now >= v.expiresAt) {
        cache.delete(k)
      }
    }
  }, 2 * 60 * 1000)
}

function stopCleanupTimer() {
  if (cleanupTimer && activeInstanceCount === 0) {
    clearInterval(cleanupTimer)
    cleanupTimer = null
  }
}

/** Destroy SWR cache and timers (for tests / micro-frontend cleanup) */
export function destroySWR() {
  if (cleanupTimer) {
    clearInterval(cleanupTimer)
    cleanupTimer = null
  }
  activeInstanceCount = 0
  cache.clear()
  pendingRequests.clear()
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
    dedupingInterval = 2000,
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

    // Deduplication: if a request completed recently (within dedupingInterval),
    // return cached data to prevent rapid duplicate calls
    if (cached && now - cached.timestamp < dedupingInterval) {
      return cached.data as T
    }

    // Make new request
    const request = client.get<T>(key, config)
      .then((res) => res.data)
      .then((data) => {
        setCacheEntry(key, data, cacheTime)
        pendingRequests.delete(key)
        return data
      })
      .catch((err) => {
        pendingRequests.delete(key)
        throw err
      })

    pendingRequests.set(key, request)

    // Timeout fallback: if request is stuck for 30s, clear the pending entry
    const timeoutId = setTimeout(() => {
      if (pendingRequests.get(key) === request) {
        pendingRequests.delete(key)
      }
    }, 30_000)

    request
      .then(() => clearTimeout(timeoutId))
      .catch(() => clearTimeout(timeoutId))

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
      error.value = null
    }
    isValidating.value = true

    try {
      const key = cacheKey.value
      if (!key) return

      const result = await fetcher(key)
      // Discard stale response — a newer request has been initiated
      if (currentVersion !== requestVersion) return
      data.value = result
      // Clear error only on success
      error.value = null
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
    const key = cacheKey.value
    if (!key) return

    if (newData !== undefined) {
      data.value = newData
      setCacheEntry(key, newData, cacheTime)
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
          executeRequest(true).catch(() => {})
        } else {
          executeRequest().catch(() => {})
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
            executeRequest(true).catch(() => {})
          }, refreshInterval)
        }
      },
      { immediate: true }
    )
    onUnmounted(() => {
      stopRefreshWatch()
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
        executeRequest(true).catch(() => {})
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
      // Check cache freshness before revalidating (consistent with onFocus behavior)
      const cached = cache.get(cacheKey.value)
      if (!cached || Date.now() > cached.timestamp + cacheTime / 2) {
        executeRequest(true).catch(() => {})
      }
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
    activeInstanceCount = Math.max(0, activeInstanceCount - 1)
    stopCleanupTimer()
  })

  // Start cleanup timer when first instance is created.
  // Increment count at the beginning (before watch immediate fires) so that
  // the onUnmounted decrement is always balanced even if watch throws.
  activeInstanceCount++
  startCleanupTimer()

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
    setCacheEntry(key, data, DEFAULT_CACHE_TIME)
    return data
  })
}

// Helper to get a cache entry (for useCachedRequest delegation)
export function getSWRCacheEntry<T>(key: string): CacheEntry<T> | undefined {
  return cache.get(key) as CacheEntry<T> | undefined
}

// Helper to write to SWR cache (for useCachedRequest delegation)
export function setSWRCache<T>(key: string, data: T, ttl: number) {
  setCacheEntry(key, data, ttl)
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
