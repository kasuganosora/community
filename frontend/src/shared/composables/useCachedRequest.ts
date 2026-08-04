import { ref, computed, onUnmounted, watch, type Ref } from 'vue'
import client from '@/shared/api/client'
import type { AxiosRequestConfig } from 'axios'
import { clearSWRCache, getSWRCacheSize, getSWRCacheEntry, setSWRCache } from './useSWR'

interface CacheOptions {
  ttl?: number // Time to live in milliseconds
  key?: string // Custom cache key
}

/**
 * @deprecated Use useSWR instead. This composable now delegates to the same
 * underlying cache as useSWR for backward compatibility, but useSWR provides
 * better features (deduplication, focus revalidation, etc).
 */
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

  // Race condition guard: only the latest request's response is applied
  let requestVersion = 0
  let disposed = false

  onUnmounted(() => {
    disposed = true
  })

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

    // Check cache unless force refresh
    if (!forceRefresh) {
      const cached = getSWRCacheEntry<T>(cacheKey.value)
      if (cached && Date.now() < cached.expiresAt) {
        data.value = cached.data
        lastFetched.value = cached.timestamp
        return cached.data
      }
    }

    isLoading.value = true
    error.value = null

    const version = ++requestVersion

    try {
      const response = await client.get<T>(currentUrl, axiosConfig)
      // Ignore stale responses or responses after unmount
      if (disposed || version !== requestVersion) return null

      const result = response.data

      data.value = result
      lastFetched.value = Date.now()
      // Write to shared SWR cache
      setSWRCache(cacheKey.value, result, ttl)
      return result
    } catch (err) {
      if (disposed || version !== requestVersion) return null
      error.value = err instanceof Error ? err : new Error(String(err))
      throw err
    } finally {
      if (!disposed && version === requestVersion) {
        isLoading.value = false
      }
    }
  }

  function invalidate() {
    clearSWRCache(cacheKey.value)
    // Mark local data as stale
    lastFetched.value = null
  }

  function updateCache(newData: T) {
    data.value = newData
    lastFetched.value = Date.now()
    // Sync to shared SWR cache so other components see the update
    setSWRCache(cacheKey.value, newData, ttl)
  }

  const isStale = computed(() => {
    if (!lastFetched.value) return true
    return Date.now() - lastFetched.value > ttl
  })

  const hasCache = computed(() => data.value !== null)

  // Auto re-fetch when url changes (only when url is a Ref)
  const stopUrlWatch = watch(
    cacheKey,
    (newKey, oldKey) => {
      if (newKey && newKey !== oldKey) {
        execute().catch(() => {})
      }
    }
  )

  onUnmounted(() => {
    disposed = true
    stopUrlWatch()
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
    refresh: () => execute(true).catch(() => null),
  }
}

// Helper to prefetch data — writes result to cache
export async function prefetchCache<T>(
  url: string,
  options: CacheOptions & AxiosRequestConfig = {}
): Promise<T> {
  const { ttl = 60000, ...axiosConfig } = options
  const { data } = await client.get<T>(url, axiosConfig)
  const cacheKey = `${url}:${JSON.stringify(axiosConfig.params || {})}`
  setSWRCache(cacheKey, data, ttl)
  return data
}

// Delegate to useSWR's cache
export function clearRequestCache(key?: string) {
  clearSWRCache(key)
}

// Helper to get cache stats
export function getCacheStats() {
  return { total: getSWRCacheSize() }
}
