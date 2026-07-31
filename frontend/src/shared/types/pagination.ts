/**
 * Cursor-based pagination parameters shared across all API endpoints.
 * Used by timeline, notifications, bookmarks, and account status listings.
 */
export interface PaginationParams {
  max_id?: string
  since_id?: string
  min_id?: string
  limit?: number
}
