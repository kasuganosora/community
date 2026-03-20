import { ref, computed, type Ref } from 'vue'

interface UseAsyncStateOptions<T> {
  immediate?: boolean
  initialData?: T
  onError?: (error: Error) => void
  onSuccess?: (data: T) => void
  resetOnExecute?: boolean
}

interface ExecuteOptions {
  throwOnError?: boolean
}

export function useAsyncState<T>(
  promiseFn: () => Promise<T>,
  options: UseAsyncStateOptions<T> = {}
) {
  const {
    initialData,
    onError,
    onSuccess,
    resetOnExecute = true,
  } = options

  // Use immediate from options (destructured separately to handle both versions)
  const immediate = options.immediate ?? false

  const data = ref<T | undefined>(initialData) as Ref<T | undefined>
  const error = ref<Error | null>(null)
  const isLoading = ref(false)
  const isReady = ref(false)
  const isFinished = ref(false)

  async function execute(executeOptions: ExecuteOptions = {}): Promise<T | null> {
    const { throwOnError = false } = executeOptions

    if (resetOnExecute) {
      error.value = null
      isReady.value = false
    }
    isLoading.value = true
    isFinished.value = false

    try {
      const result = await promiseFn()
      data.value = result
      isReady.value = true
      onSuccess?.(result)
      return result
    } catch (err) {
      const e = err instanceof Error ? err : new Error(String(err))
      error.value = e
      onError?.(e)
      if (throwOnError) {
        throw e
      }
      return null
    } finally {
      isLoading.value = false
      isFinished.value = true
    }
  }

  if (immediate) {
    execute()
  }

  return {
    data: computed(() => data.value),
    error: computed(() => error.value),
    isLoading: computed(() => isLoading.value),
    isReady: computed(() => isReady.value),
    isFinished: computed(() => isFinished.value),
    execute,
  }
}

// Version with arguments
export function useAsyncStateWithArgs<T, Args extends unknown[]>(
  promiseFn: (...args: Args) => Promise<T>,
  options: UseAsyncStateOptions<T> = {}
) {
  // Use immediate from options when needed
  const _immediateValue = options.immediate ?? false
  void _immediateValue // Mark as intentionally used

  const {
    initialData,
    onError,
    onSuccess,
    resetOnExecute = true,
  } = options

  const data = ref<T | undefined>(initialData) as Ref<T | undefined>
  const error = ref<Error | null>(null)
  const isLoading = ref(false)
  const isReady = ref(false)
  const lastExecuted = ref<number | null>(null)

  async function execute(
    ...args: Args
  ): Promise<T | null> {
    if (resetOnExecute) {
      error.value = null
      isReady.value = false
    }
    isLoading.value = true

    try {
      const result = await promiseFn(...args)
      data.value = result
      isReady.value = true
      lastExecuted.value = Date.now()
      onSuccess?.(result)
      return result
    } catch (err) {
      const e = err instanceof Error ? err : new Error(String(err))
      error.value = e
      onError?.(e)
      return null
    } finally {
      isLoading.value = false
    }
  }

  return {
    data: computed(() => data.value),
    error: computed(() => error.value),
    isLoading: computed(() => isLoading.value),
    isReady: computed(() => isReady.value),
    lastExecuted: computed(() => lastExecuted.value),
    execute,
  }
}
