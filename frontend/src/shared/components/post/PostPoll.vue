<script setup lang="ts">
import { ref, computed } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import type { MastodonPoll } from '@/shared/types/mastodon'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { pollOptionPercent, pollShowsResults } from '@/domains/post/domain/poll'
import RelativeTime from '@/shared/components/common/RelativeTime.vue'

const props = defineProps<{
  poll: MastodonPoll
  /** True when the poll belongs to the current user (results are always visible). */
  own?: boolean
}>()

const emit = defineEmits<{
  updated: [poll: MastodonPoll]
}>()

const selected = ref<number[]>([])
const voting = ref(false)
const revealed = ref(false)
const name = `poll-${props.poll.id}-${Math.random().toString(36).slice(2, 6)}`

const showResults = computed(() => revealed.value || pollShowsResults(props.poll, props.own))
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
    MessagePlugin.error(e instanceof Error && e.message ? `投票失败：${e.message}` : '投票失败')
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
        <span class="post-poll__title">{{ opt.title }}</span>
        <t-icon v-if="poll.own_votes?.includes(i)" name="check-circle" :size="14" />
      </li>
    </ul>
    <fieldset v-else class="post-poll__form">
      <legend class="sr-only">投票选项</legend>
      <label v-for="(opt, i) in poll.options" :key="i" class="post-poll__choice">
        <input
          :type="poll.multiple ? 'checkbox' : 'radio'"
          :name="name"
          :checked="selected.includes(i)"
          @change="toggle(i)"
        />
        <span>{{ opt.title }}</span>
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
        投票
      </t-button>
      <span>{{ total }} 人参与</span>
      <span v-if="poll.expired">· 已结束</span>
      <span v-else-if="poll.expires_at">· <RelativeTime :datetime="poll.expires_at" future /> 后结束</span>
      <button v-if="!showResults" type="button" class="post-poll__link" @click="revealed = true">查看结果</button>
      <button v-else-if="revealed && !poll.voted && !poll.expired" type="button" class="post-poll__link" @click="revealed = false">返回投票</button>
    </div>
  </div>
</template>

<style scoped>
.post-poll {
  margin-top: var(--space-2);
}

.post-poll__results,
.post-poll__form {
  list-style: none;
  margin: 0;
  padding: 0;
  border: none;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.post-poll__result {
  position: relative;
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm, 4px);
  overflow: hidden;
  font-size: var(--font-sm);
}

.post-poll__bar {
  position: absolute;
  inset: 0 auto 0 0;
  background: var(--bg-hover);
  z-index: 0;
}

.post-poll__result--own .post-poll__bar {
  background: var(--accent-blue-light, rgba(29, 155, 240, 0.2));
}

.post-poll__pct,
.post-poll__title,
.post-poll__result :deep(svg) {
  position: relative;
  z-index: 1;
}

.post-poll__pct {
  font-weight: var(--font-bold, 700);
  min-width: 3em;
}

.post-poll__choice {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) var(--space-2);
  border: 1px solid var(--border-color);
  border-radius: var(--radius-full);
  cursor: pointer;
  font-size: var(--font-sm);
}

.post-poll__footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin-top: var(--space-2);
  font-size: var(--font-xs);
  color: var(--text-secondary);
}

.post-poll__link {
  color: var(--accent-blue);
  font-size: var(--font-xs);
}
</style>
