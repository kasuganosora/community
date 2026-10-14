import { isAxiosError } from 'axios'

// Polling an existing join is safe to retry after a transport/server failure.
// Preserve explicit terminal errors, cancellation, and the caller's deadline.
export function isRetryableGroupRequestError(error: unknown): boolean {
  if (!isAxiosError<{ retryable?: boolean }>(error) || error.code === 'ERR_CANCELED' || error.response?.data?.retryable === false) return false
  return !error.response || error.response.status >= 500 || error.response.status === 429
}
