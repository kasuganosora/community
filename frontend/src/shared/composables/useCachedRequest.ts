import { ref, computed, type Ref } from 'vue'
import client from '@/shared/api/client'
import type { AxiosRequestConfig } from 'axios'
import { clearSWRCache, getSWRCacheSize } from './useSWR'

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

    isLoading.value = true
    error.value = null

    try {
      const response = await client.get<T>(currentUrl, axiosConfig)
      const result = response.data

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
    clearSWRCache(cacheKey.value)
  }

  function updateCache(newData: T) {
    data.value = newData
    lastFetched.value = Date.now()
  }

  const isStale = computed(() => {
    if (!lastFetched.value) return true
    return Date.now() - lastFetched.value > ttl
  })

  const hasCache = computed(() => data.value !== null)

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
  _options: CacheOptions & AxiosRequestConfig = {}
): Promise<T> {
  const { data } = await client.get<T>(url)
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
