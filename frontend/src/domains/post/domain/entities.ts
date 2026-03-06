export interface CreateStatusParams {
  status: string
  in_reply_to_id?: string
  media_ids?: string[]
  poll?: {
    options: string[]
    expires_in: number
    multiple?: boolean
  }
  sensitive?: boolean
  spoiler_text?: string
  visibility?: 'public' | 'unlisted' | 'private' | 'direct'
}
