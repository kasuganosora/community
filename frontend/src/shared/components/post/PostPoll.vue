<script setup lang="ts">
import { isNoDeadlinePoll } from '@/domains/post/domain/compose'
import { ref, computed } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import type { MastodonPoll } from '@/shared/types/mastodon'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { pollOptionPercent, pollShowsResults } from '@/domains/post/domain/poll'
import RelativeTime from '@/shared/components/common/RelativeTime.vue'
import EmojiText from '@/shared/components/common/EmojiText.vue'

import { t } from '@/shared/i18n'
const props = defineProps<{
  poll: MastodonPoll
}>()

const emit = defineEmits<{
  updated: [poll: MastodonPoll]
}>()

const selected = ref<number[]>([])
const voting = ref(false)
const revealed = ref(false)
const name = `poll-${props.poll.id}-${Math.random().toString(36).slice(2, 6)}`

const showResults = computed(() => revealed.value || pollShowsResults(props.poll))
const total = computed(() => props.poll.multiple ? (props.poll.voters_count ?? props.poll.votes_count) : props.poll.votes_count)

function toggle(i: number) {
  if (props.poll.multiple) {
    selected.value = selected.value.includes(i) ? selected.value.filter(x => x !== i) : [...selected.value, i]
  } else {
    selected.value = [i]
  }
}

async function vote() {
  if (!selected.value.length || voting.value) return
  voting.value = true
  try {
    const updated = await postApi.votePoll(props.poll.id, [...selected.value].sort((a, b) => a - b))
    emit('updated', updated)
  } catch (e) {
    MessagePlugin.error(e instanceof Error && e.message ? t('shared.PostPoll.s8', [e.message]) : t('shared.PostPoll.s9'))
  } finally {
    voting.value = false
  }
}
</script>

<template>
  <div class="post-poll" @click.stop>
    <ul v-if="showResults" class="post-poll__results">
      <li
        v-for="(opt, i) in poll.options"
        :key="i"
        class="post-poll__result"
        :class="{ 'post-poll__result--own': poll.own_votes?.includes(i) }"
      >
        <div class="post-poll__bar" :style="{ width: pollOptionPercent(opt.votes_count, total) + '%' }" />
        <span class="post-poll__pct">{{ pollOptionPercent(opt.votes_count, total) }}%</span>
        <span class="post-poll__title"><EmojiText :text="opt.title" :emojis="poll.emojis" /></span>
        <t-icon v-if="poll.own_votes?.includes(i)" name="check-circle" :size="14" />
      </li>
    </ul>
    <fieldset v-else class="post-poll__form">
      <legend class="sr-only">{{ $t('shared.PostPoll.s1') }}</legend>
      <label v-for="(opt, i) in poll.options" :key="i" class="post-poll__choice" @click.prevent="toggle(i)">
        <input
          :type="poll.multiple ? 'checkbox' : 'radio'"
          :name="name"
          :checked="selected.includes(i)"
        />
        <span><EmojiText :text="opt.title" :emojis="poll.emojis" /></span>
      </label>
    </fieldset>
    <div class="post-poll__footer">
      <t-button
        v-if="!showResults"
        size="small"
        shape="round"
        variant="outline"
        :disabled="!selected.length"
        :loading="voting"
        @click="vote"
      >
        {{ $t('shared.PostPoll.s2') }}
      </t-button>
      <span>{{ $t('shared.PostPoll.s3', [total]) }}</span>
      <span v-if="poll.expired">{{ $t('shared.PostPoll.s4') }}</span>
      <span v-else-if="isNoDeadlinePoll(poll.expires_at)">{{ $t('shared.PostPoll.noDeadline') }}</span>
      <span v-else-if="poll.expires_at">· <RelativeTime :datetime="poll.expires_at" future /> {{ $t('shared.PostPoll.s5') }}</span>
      <button v-if="!showResults" type="button" class="post-poll__link" @click="revealed = true">{{ $t('shared.PostPoll.s6') }}</button>
      <button v-else-if="revealed && !poll.voted && !poll.expired" type="button" class="post-poll__link" @click="revealed = false">{{ $t('shared.PostPoll.s7') }}</button>
    </div>
  </div>
</template>

<style scoped>
.post-poll {
  margin-top: var(--space-2);
  max-width: 28rem;
}

.post-poll__results,
.post-poll__form {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  border: none;
  list-style: none;
}

/* Results: a filled track with the share drawn as a softer bar inside. */
.post-poll__result {
  position: relative;
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 2.25rem;
  padding: 0 var(--space-3);
  border-radius: var(--radius-sm);
  background: var(--color-fill);
  overflow: hidden;
  font-size: var(--text-subhead);
  color: var(--color-label);
}

.post-poll__bar {
  position: absolute;
  inset: 0 auto 0 0;
  z-index: 0;
  background: var(--color-fill-strong);
}

.post-poll__result--own .post-poll__bar {
  background: var(--color-accent-soft);
}

.post-poll__result--own {
  font-weight: var(--weight-medium);
}

.post-poll__result :deep(svg) {
  color: var(--color-accent-text);
  margin-left: auto;
}

.post-poll__pct,
.post-poll__title,
.post-poll__result :deep(svg) {
  position: relative;
  z-index: 1;
}

.post-poll__pct {
  min-width: 2.75em;
  font-weight: var(--weight-semibold);
  font-variant-numeric: tabular-nums;
}

/* Choices: capsule rows with a 44pt-friendly height. */
.post-poll__choice {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 2.5rem;
  padding: 0 var(--space-3);
  border-radius: var(--radius-sm);
  box-shadow: inset 0 0 0 1px var(--color-separator-opaque);
  font-size: var(--text-subhead);
  color: var(--color-label);
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: background-color var(--duration-release) var(--ease-out);
}

.post-poll__choice:active {
  background: var(--color-pressed);
  transition-duration: 0s;
}

.post-poll__choice:has(input:checked) {
  box-shadow: inset 0 0 0 1.5px var(--color-accent);
  background: var(--color-accent-soft);
}

.post-poll__choice input {
  width: 1.125rem;
  height: 1.125rem;
  accent-color: var(--color-accent);
}

.post-poll__footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-2);
  margin-top: var(--space-2);
  font-size: var(--text-footnote);
  color: var(--color-label-secondary);
}

.post-poll__link {
  min-height: 2rem;
  color: var(--color-accent-text);
  font-size: var(--text-footnote);
  font-weight: var(--weight-medium);
}
</style>
