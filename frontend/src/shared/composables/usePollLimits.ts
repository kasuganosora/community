import { ref } from 'vue'
import client from '@/shared/api/client'
import { DEFAULT_POLL_LIMITS, type PollLimits } from '@/domains/post/domain/compose'

interface InstancePolls {
  configuration?: {
    polls?: {
      max_options?: number
      max_characters_per_option?: number
      min_expiration?: number
      max_expiration?: number
    }
  }
}

const limits = ref<PollLimits>({ ...DEFAULT_POLL_LIMITS })
let pending: Promise<void> | null = null

function positive(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : fallback
}

/** Poll limits from the instance configuration, loaded once per page. */
export function usePollLimits() {
  if (!pending) {
    pending = client.get<InstancePolls>('/api/v2/instance')
      .then(({ data }) => {
        const polls = data?.configuration?.polls ?? {}
        limits.value = {
          maxOptions: positive(polls.max_options, DEFAULT_POLL_LIMITS.maxOptions),
          maxCharactersPerOption: positive(polls.max_characters_per_option, DEFAULT_POLL_LIMITS.maxCharactersPerOption),
          minExpiration: positive(polls.min_expiration, DEFAULT_POLL_LIMITS.minExpiration),
          maxExpiration: positive(polls.max_expiration, DEFAULT_POLL_LIMITS.maxExpiration),
        }
      })
      .catch(() => { pending = null })
  }
  return limits
}
