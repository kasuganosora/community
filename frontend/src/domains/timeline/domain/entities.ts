export interface TimelineCursor {
  max_id?: string
  since_id?: string
  min_id?: string
  limit?: number
}

export type TimelineType = 'home' | 'public' | 'tag' | 'list'
