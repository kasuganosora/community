import type { PaginationParams } from '@/shared/types/pagination'

/**
 * Timeline cursor parameters — alias for the shared PaginationParams.
 * Kept for domain-specific semantic clarity.
 */
export type TimelineCursor = PaginationParams

/**
 * All timeline types supported by the API.
 * The store currently only implements 'home' and 'public';
 * 'tag' and 'list' are defined for API completeness.
 */
export type TimelineType = 'home' | 'public' | 'tag' | 'list'

/** Timeline types that the store actually manages */
export type ManagedTimelineType = Exclude<TimelineType, 'tag' | 'list'>
