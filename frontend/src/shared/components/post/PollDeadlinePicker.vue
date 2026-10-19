<script setup lang="ts">
import { useDismiss } from '@/shared/composables/useDismiss'
import { computed, ref } from 'vue'
import { POLL_DURATIONS, POLL_DURATION_UNITS, formatPollDuration, splitPollDuration, type PollDeadlineMode, type PollDurationUnit, type PollLimits } from '@/domains/post/domain/compose'
import { currentLocale, t } from '@/shared/i18n'

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
  return POLL_DURATIONS.find(d => d.value === props.expiresIn)?.label ?? formatPollDuration(props.expiresIn, currentLocale())
})

const isPreset = computed(() => durations.value.some(d => d.value === props.expiresIn))
const customAmount = ref(splitPollDuration(props.expiresIn).amount)
const customUnit = ref<PollDurationUnit>(splitPollDuration(props.expiresIn).unit)
const units = computed(() => (Object.keys(POLL_DURATION_UNITS) as PollDurationUnit[]).map(u => ({
  value: u,
  label: new Intl.NumberFormat(currentLocale(), { style: 'unit', unit: u, unitDisplay: 'long' }).formatToParts(2).find(p => p.type === 'unit')?.value.trim() ?? u,
})))
const customSeconds = computed(() => Math.floor(Number(customAmount.value) || 0) * POLL_DURATION_UNITS[customUnit.value])
const customError = computed(() => {
  const s = customSeconds.value
  if (s < props.limits.minExpiration) return t('shared.PostComposer.pollCustomTooShort', [formatPollDuration(props.limits.minExpiration, currentLocale())])
  if (props.limits.maxExpiration > 0 && s > props.limits.maxExpiration) return t('shared.PostComposer.pollCustomTooLong', [formatPollDuration(props.limits.maxExpiration, currentLocale())])
  return ''
})
function applyCustom() {
  if (customError.value) return
  emit('update:mode', 'duration')
  emit('update:expiresIn', customSeconds.value)
  open.value = false
}

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
    <Transition name="deadline-pop">
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
      <form v-if="mode === 'duration'" class="deadline__custom" novalidate @submit.prevent="applyCustom">
        <span class="deadline__custom-label">{{ $t('shared.PostComposer.pollCustomDuration') }}</span>
        <div class="deadline__custom-row" :class="{ 'deadline__custom-row--active': !isPreset }">
          <input
            v-model.number="customAmount"
            type="number"
            inputmode="numeric"
            min="1"
            step="1"
            :aria-label="$t('shared.PostComposer.pollCustomAmount')"
            :aria-invalid="!!customError"
            data-testid="poll-custom-amount"
          />
          <select v-model="customUnit" :aria-label="$t('shared.PostComposer.pollCustomUnit')" data-testid="poll-custom-unit">
            <option v-for="u in units" :key="u.value" :value="u.value">{{ u.label }}</option>
          </select>
          <button type="submit" class="deadline__apply" :disabled="!!customError" data-testid="poll-custom-apply">
            {{ $t('shared.PostComposer.pollCustomApply') }}
          </button>
        </div>
        <p v-if="customError" class="deadline__error" role="alert">{{ customError }}</p>
      </form>
      <label v-else-if="mode === 'at'" class="deadline__at">
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
    </Transition>
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
  max-height: min(12rem, 32dvh);
  overflow-y: auto;
  overscroll-behavior: contain;
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

.deadline__custom {
  margin-top: var(--space-2);
  padding-top: var(--space-2);
  border-top: 1px solid var(--color-separator);
}

.deadline__custom-label {
  display: block;
  margin: 0 var(--space-1) var(--space-1);
  font-size: var(--text-footnote);
  color: var(--color-label-secondary);
}

.deadline__custom-row {
  display: flex;
  gap: var(--space-1);
}

.deadline__custom-row input,
.deadline__custom-row select {
  min-width: 0;
  min-height: 2.5rem;
  padding: 0 var(--space-2);
  border: none;
  border-radius: var(--radius-sm);
  background: var(--color-fill);
  color: var(--color-label);
  font: inherit;
  font-size: var(--text-subhead);
  color-scheme: light dark;
}

.deadline__custom-row input { flex: 1 1 4rem; font-variant-numeric: tabular-nums; }
.deadline__custom-row select { flex: 1 1 5rem; cursor: pointer; }
.deadline__custom-row option { background: var(--color-bg-elevated); color: var(--color-label); }
.deadline__custom-row input:focus-visible,
.deadline__custom-row select:focus-visible { outline: none; box-shadow: 0 0 0 3px var(--color-focus-ring); }
.deadline__custom-row--active input { box-shadow: inset 0 0 0 1px var(--color-accent); }

.deadline__apply {
  flex: none;
  min-height: 2.5rem;
  padding: 0 var(--space-3);
  border-radius: var(--radius-sm);
  background: var(--color-accent);
  color: #fff;
  font-size: var(--text-subhead);
  font-weight: var(--weight-semibold);
  transition: transform 120ms var(--ease-out), opacity 120ms var(--ease-out);
}

.deadline__apply:active:not(:disabled) { transform: scale(0.97); }
.deadline__apply:disabled { opacity: 0.4; cursor: not-allowed; }

.deadline__error {
  margin: var(--space-1) var(--space-1) 0;
  font-size: var(--text-caption);
  color: var(--color-error, var(--color-warning));
}

.deadline__at input { color-scheme: light dark; }

/* Pop from the trigger; short and ease-out so it never feels sluggish. */
.deadline__panel { transform-origin: top right; }
.deadline-pop-enter-active { transition: opacity 150ms var(--ease-out), transform 150ms var(--ease-out); }
.deadline-pop-leave-active { transition: opacity 100ms ease-in, transform 100ms ease-in; }
.deadline-pop-enter-from,
.deadline-pop-leave-to { opacity: 0; transform: scale(0.96); }

@media (prefers-reduced-motion: reduce) {
  .deadline-pop-enter-active,
  .deadline-pop-leave-active { transition: opacity 100ms linear; }
  .deadline-pop-enter-from,
  .deadline-pop-leave-to { transform: none; }
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

  .deadline-pop-enter-from,
  .deadline-pop-leave-to { transform: translateY(16px); }

  .deadline-pop-enter-active { transition-duration: 220ms; }

  .deadline__list {
    max-height: 40dvh;
  }

  .deadline__option {
    min-height: var(--hit-target, 2.75rem);
  }
}

.deadline__at input:focus-visible { outline: none; box-shadow: 0 0 0 3px var(--color-focus-ring); }
</style>
