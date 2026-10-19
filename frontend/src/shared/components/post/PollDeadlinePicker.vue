<script setup lang="ts">
import { useDismiss } from '@/shared/composables/useDismiss'
import { computed, ref } from 'vue'
import { POLL_DURATIONS, type PollDeadlineMode, type PollLimits } from '@/domains/post/domain/compose'
import { t } from '@/shared/i18n'

const props = withDefaults(defineProps<{
  mode?: PollDeadlineMode
  expiresIn: number
  endsAt?: string
  limits: PollLimits
}>(), { mode: 'duration', endsAt: '' })
const emit = defineEmits<{
  'update:mode': [PollDeadlineMode]
  'update:expiresIn': [number]
  'update:endsAt': [string]
}>()

const open = ref(false)
const root = ref<HTMLElement>()

const durations = computed(() => POLL_DURATIONS.filter(d =>
  d.value >= props.limits.minExpiration && (props.limits.maxExpiration <= 0 || d.value <= props.limits.maxExpiration)))

const modes = computed<{ value: PollDeadlineMode; label: string }[]>(() => [
  { value: 'none', label: t('shared.PostComposer.pollNoDeadline') },
  { value: 'duration', label: t('shared.PostComposer.pollModeDuration') },
  { value: 'at', label: t('shared.PostComposer.pollModeAt') },
])

function localInput(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}
const minAt = computed(() => localInput(new Date(Date.now() + props.limits.minExpiration * 1000 + 60000)))
const maxAt = computed(() => props.limits.maxExpiration > 0 ? localInput(new Date(Date.now() + props.limits.maxExpiration * 1000)) : undefined)

const triggerLabel = computed(() => {
  if (props.mode === 'none') return t('shared.PostComposer.pollNoDeadline')
  if (props.mode === 'at') {
    const d = props.endsAt ? new Date(props.endsAt) : null
    return d && !Number.isNaN(d.getTime())
      ? t('shared.PostComposer.pollEndsAtShort', [d.toLocaleString(undefined, { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })])
      : t('shared.PostComposer.pollModeAt')
  }
  return POLL_DURATIONS.find(d => d.value === props.expiresIn)?.label ?? t('shared.PostComposer.s16')
})

function selectMode(mode: PollDeadlineMode) {
  emit('update:mode', mode)
  if (mode === 'at' && !props.endsAt) emit('update:endsAt', localInput(new Date(Date.now() + 86400000)))
  if (mode === 'none') open.value = false
}
function selectDuration(value: number) {
  emit('update:mode', 'duration')
  emit('update:expiresIn', value)
  open.value = false
}

useDismiss(open, () => root.value)
</script>

<template>
  <div ref="root" class="deadline">
    <button
      type="button"
      class="deadline__trigger"
      :aria-label="$t('shared.PostComposer.s16')"
      :aria-expanded="open"
      aria-haspopup="dialog"
      data-testid="poll-deadline"
      @click="open = !open"
    >
      <t-icon name="time" :size="16" />
      <span class="deadline__value">{{ triggerLabel }}</span>
      <t-icon name="chevron-down" :size="14" />
    </button>
    <div v-if="open" class="deadline__panel" role="dialog" :aria-label="$t('shared.PostComposer.s16')">
      <div class="deadline__modes" role="radiogroup" :aria-label="$t('shared.PostComposer.pollDeadlineMode')">
        <button
          v-for="m in modes"
          :key="m.value"
          type="button"
          role="radio"
          class="deadline__mode"
          :class="{ 'deadline__mode--active': mode === m.value }"
          :aria-checked="mode === m.value"
          @click="selectMode(m.value)"
        >
          {{ m.label }}
        </button>
      </div>
      <p v-if="mode === 'none'" class="deadline__hint">{{ $t('shared.PostComposer.pollNoDeadlineHint') }}</p>
      <ul v-else-if="mode === 'duration'" class="deadline__list" role="listbox" :aria-label="$t('shared.PostComposer.pollModeDuration')">
        <li v-for="d in durations" :key="d.value">
          <button
            type="button"
            role="option"
            class="deadline__option"
            :class="{ 'deadline__option--active': expiresIn === d.value }"
            :aria-selected="expiresIn === d.value"
            @click="selectDuration(d.value)"
          >
            <span>{{ d.label }}</span>
            <t-icon v-if="expiresIn === d.value" name="check" :size="16" />
          </button>
        </li>
      </ul>
      <label v-else class="deadline__at">
        <span>{{ $t('shared.PostComposer.pollEndsAtLabel') }}</span>
        <input
          type="datetime-local"
          :value="endsAt"
          :min="minAt"
          :max="maxAt"
          @input="emit('update:endsAt', ($event.target as HTMLInputElement).value)"
        />
      </label>
    </div>
  </div>
</template>

<style scoped>
.deadline {
  position: relative;
  display: inline-flex;
}

.deadline__trigger {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-height: 2.25rem;
  padding: 0 var(--space-3);
  border-radius: var(--radius-full);
  background: var(--color-fill);
  color: var(--color-label);
  font: inherit;
  font-size: var(--text-subhead);
  font-weight: var(--weight-medium);
  max-width: 100%;
}

.deadline__trigger:hover { background: var(--color-fill-strong); }
.deadline__trigger:focus-visible { outline: none; box-shadow: 0 0 0 3px var(--color-focus-ring); }

.deadline__value {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.deadline__panel {
  position: absolute;
  z-index: 30;
  top: calc(100% + var(--space-1));
  right: 0;
  width: min(18rem, calc(100vw - 2rem));
  padding: var(--space-2);
  border-radius: var(--radius-md);
  background: var(--color-bg-elevated);
  box-shadow: var(--shadow-popover);
  border: 1px solid var(--color-separator);
  color: var(--color-label);
}

.deadline__modes {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 2px;
  padding: 2px;
  border-radius: var(--radius-sm);
  background: var(--color-fill);
}

.deadline__mode {
  min-height: 2rem;
  padding: 0 var(--space-1);
  border-radius: calc(var(--radius-sm) - 2px);
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  font-weight: var(--weight-medium);
}

.deadline__mode--active {
  background: var(--color-bg-row);
  color: var(--color-label);
  box-shadow: 0 1px 2px rgb(0 0 0 / 25%);
}

.deadline__hint {
  margin: var(--space-2) var(--space-1) var(--space-1);
  font-size: var(--text-footnote);
  color: var(--color-label-secondary);
}

.deadline__list {
  margin: var(--space-2) 0 0;
  padding: 0;
  list-style: none;
  max-height: 16rem;
  overflow-y: auto;
}

.deadline__option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  min-height: 2.5rem;
  padding: 0 var(--space-2);
  border-radius: var(--radius-sm);
  color: var(--color-label);
  font-size: var(--text-subhead);
  text-align: left;
}

.deadline__option:hover { background: var(--color-hover); }
.deadline__option--active { color: var(--color-accent-text); }

.deadline__at {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin-top: var(--space-2);
  font-size: var(--text-footnote);
  color: var(--color-label-secondary);
}

.deadline__at input {
  min-height: 2.5rem;
  padding: 0 var(--space-2);
  border: none;
  border-radius: var(--radius-sm);
  background: var(--color-fill);
  color: var(--color-label);
  font: inherit;
  font-size: var(--text-subhead);
}

/* Narrow screens: a bottom sheet keeps every choice reachable. */
@media (max-width: 600px) {
  .deadline__panel {
    position: fixed;
    top: auto;
    right: 0;
    bottom: 0;
    left: 0;
    width: auto;
    padding: var(--space-3) var(--space-3) calc(var(--space-3) + env(safe-area-inset-bottom));
    border-radius: var(--radius-lg, 16px) var(--radius-lg, 16px) 0 0;
    border-width: 1px 0 0;
  }

  .deadline__list {
    max-height: 50dvh;
  }

  .deadline__option {
    min-height: var(--hit-target, 2.75rem);
  }
}

.deadline__at input:focus-visible { outline: none; box-shadow: 0 0 0 3px var(--color-focus-ring); }
</style>
