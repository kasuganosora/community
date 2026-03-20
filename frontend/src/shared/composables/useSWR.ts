import { ref, shallowRef, computed, watch } from 'vue'
import type { AxiosRequestConfig } from 'axios'
import client from '@/shared/api/client'

// Cache storage
interface CacheEntry<T> {
  data: T
  timestamp: number
  key: string
}

const cache = new Map<string, CacheEntry<unknown>>()
const pendingRequests = new Map<string, Promise<unknown>>()

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

// SWR hook types

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

    if (cached && now - cached.timestamp < cacheTime) {
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
        // Update cache
        cache.set(key, {
          data,
          timestamp: Date.now(),
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

  // Execute request
  async function executeRequest(silent = false) {
    if (!silent) {
      isLoading.value = true
    }
    isValidating.value = true
    error.value = null

    try {
      const key = cacheKey.value
      if (!key) return

      const result = await fetcher(key)
      data.value = result
    } catch (err) {
      error.value = err instanceof Error ? err : new Error(String(err))
      if (!silent) {
        // Only throw on initial load, not on background refresh
        throw err
      }
    } finally {
      isLoading.value = false
      isValidating.value = false
    }
  }

  // Manual revalidation
  async function mutate(newData?: T, shouldRevalidate = true) {
    if (newData !== undefined) {
      data.value = newData
      cache.set(cacheKey.value, {
        data: newData,
        timestamp: Date.now(),
        key: cacheKey.value,
      })
    }

    if (shouldRevalidate) {
      await executeRequest(true)
    }
  }

  // Watch for key changes
  watch(
    cacheKey,
    (newKey, oldKey) => {
      if (newKey && newKey !== oldKey) {
        // Check cache immediately
        const cached = cache.get(newKey)
        if (cached && Date.now() - cached.timestamp < cacheTime) {
          data.value = cached.data as T
          isLoading.value = false
          // Background refresh
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
    watch(
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
  }

  // Revalidate on window focus
  if (revalidateOnFocus) {
    const onFocus = () => {
      const cached = cache.get(cacheKey.value)
      if (!cached || Date.now() - cached.timestamp > cacheTime / 2) {
        executeRequest(true)
      }
    }

    watch(
      () => cacheKey.value,
      (key, oldKey) => {
        if (oldKey) {
          window.removeEventListener('focus', onFocus)
        }
        if (key) {
          window.addEventListener('focus', onFocus)
        }
      },
      { immediate: true }
    )
  }

  // Revalidate on reconnect
  if (revalidateOnReconnect) {
    const onOnline = () => {
      executeRequest(true)
    }

    watch(
      () => cacheKey.value,
      (key, oldKey) => {
        if (oldKey) {
          window.removeEventListener('online', onOnline)
        }
        if (key) {
          window.addEventListener('online', onOnline)
        }
      },
      { immediate: true }
    )
  }

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
  if (cache.has(key)) return Promise.resolve(cache.get(key)!.data as T)

  const defaultFetcher = async (k: string) => {
    const { data } = await client.get<T>(k)
    return data
  }

  const f = fetcher || defaultFetcher

  return f(key).then((data) => {
    cache.set(key, {
      data,
      timestamp: Date.now(),
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
