import { ref, computed, type Ref } from 'vue'
import client from '@/shared/api/client'
import type { AxiosRequestConfig } from 'axios'

interface CacheEntry<T> {
  data: T
  timestamp: number
  expiresAt: number
}

interface CacheOptions {
  ttl?: number // Time to live in milliseconds
  key?: string // Custom cache key
}

// Global cache store
const globalCache = new Map<string, CacheEntry<unknown>>()

export function useCachedRequest<T>(
  url: Ref<string> | string,
  options: CacheOptions & AxiosRequestConfig = {}
) {
  const {
    ttl = 60000, // Default 1 minute
    key: customKey,
    ...axiosConfig
  } = options

  const data = ref<T | null>(null)
  const error = ref<Error | null>(null)
  const isLoading = ref(false)
  const lastFetched = ref<number | null>(null)

  const cacheKey = computed(() => {
    if (customKey) return customKey
    const urlValue = typeof url === 'string' ? url : url.value
    return `${urlValue}:${JSON.stringify(axiosConfig.params || {})}`
  })

  async function execute(forceRefresh = false): Promise<T | null> {
    const currentUrl = typeof url === 'string' ? url : url.value

    if (!currentUrl) {
      return null
    }

    // Check cache
    if (!forceRefresh) {
      const cached = globalCache.get(cacheKey.value)
      if (cached && Date.now() < cached.expiresAt) {
        data.value = cached.data as T
        lastFetched.value = cached.timestamp
        return data.value
      }
    }

    isLoading.value = true
    error.value = null

    try {
      const response = await client.get<T>(currentUrl, axiosConfig)
      const result = response.data

      // Update cache
      globalCache.set(cacheKey.value, {
        data: result,
        timestamp: Date.now(),
        expiresAt: Date.now() + ttl,
      })

      data.value = result
      lastFetched.value = Date.now()
      return result
    } catch (err) {
      error.value = err instanceof Error ? err : new Error(String(err))
      throw err
    } finally {
      isLoading.value = false
    }
  }

  function invalidate() {
    globalCache.delete(cacheKey.value)
  }

  function updateCache(newData: T) {
    globalCache.set(cacheKey.value, {
      data: newData,
      timestamp: Date.now(),
      expiresAt: Date.now() + ttl,
    })
    data.value = newData
  }

  // Computed to check if data is stale
  const isStale = computed(() => {
    if (!lastFetched.value) return true
    return Date.now() - lastFetched.value > ttl
  })

  // Computed to check if we have cached data
  const hasCache = computed(() => {
    const cached = globalCache.get(cacheKey.value)
    return cached !== undefined && Date.now() < cached.expiresAt
  })

  return {
    data: computed(() => data.value),
    error: computed(() => error.value),
    isLoading: computed(() => isLoading.value),
    isStale,
    hasCache,
    lastFetched: computed(() => lastFetched.value),
    execute,
    invalidate,
    updateCache,
    refresh: () => execute(true),
  }
}

// Helper to prefetch data
export async function prefetchCache<T>(
  url: string,
  options: CacheOptions & AxiosRequestConfig = {}
): Promise<T> {
  const { ttl = 60000, key: customKey, ...axiosConfig } = options
  const cacheKey = customKey || url

  // Check if already cached
  const cached = globalCache.get(cacheKey)
  if (cached && Date.now() < cached.expiresAt) {
    return cached.data as T
  }

  const { data } = await client.get<T>(url, axiosConfig)

  globalCache.set(cacheKey, {
    data,
    timestamp: Date.now(),
    expiresAt: Date.now() + ttl,
  })

  return data
}

// Helper to clear cache
export function clearRequestCache(key?: string) {
  if (key) {
    globalCache.delete(key)
  } else {
    globalCache.clear()
  }
}

// Helper to get cache stats
export function getCacheStats() {
  const now = Date.now()
  let valid = 0
  let expired = 0

  for (const [, entry] of globalCache) {
    if (now < entry.expiresAt) {
      valid++
    } else {
      expired++
    }
  }

  return {
    total: globalCache.size,
    valid,
    expired,
  }
}

// Cleanup expired entries periodically
if (typeof window !== 'undefined') {
  setInterval(() => {
    const now = Date.now()
    for (const [key, entry] of globalCache) {
      if (now > entry.expiresAt) {
        globalCache.delete(key)
      }
    }
  }, 60000) // Clean up every minute
}
