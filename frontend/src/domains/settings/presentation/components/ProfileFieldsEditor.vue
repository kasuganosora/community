<script setup lang="ts">
import { computed, nextTick, ref, toRaw, useId, watch } from 'vue'
import { t } from '@/shared/i18n'

type Field = { name: string; value: string }
type Row = Field & { key: string }
const props = defineProps<{ modelValue: Field[]; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [fields: Field[]] }>()
const editorId = useId()
let sequence = 0
let lastEmitted: Field[] | undefined
const rows = ref<Row[]>([])
const dragged = ref<string | null>(null)
const announcement = ref('')
const filledCount = computed(() => rows.value.filter(row => row.name.trim() || row.value.trim()).length)
watch(() => props.modelValue, fields => {
  if (toRaw(fields) === lastEmitted) return
  rows.value = fields.map(field => ({ ...field, key: `${editorId}-${++sequence}` }))
  dragged.value = null
}, { immediate: true })
watch(() => props.disabled, disabled => { if (disabled) dragged.value = null })
function publish() {
  lastEmitted = rows.value.map(({ name, value }) => ({ name, value }))
  emit('update:modelValue', lastEmitted)
}
function update(row: Row, field: keyof Field, event: Event) {
  if (props.disabled) return
  row[field] = (event.target as HTMLInputElement).value
  publish()
}
async function add() {
  if (props.disabled || rows.value.length >= 16) return
  const row = { key: `${editorId}-${++sequence}`, name: '', value: '' }
  rows.value.push(row)
  publish()
  await nextTick()
  document.getElementById(`${row.key}-name`)?.focus()
}
async function remove(index: number) {
  if (props.disabled) return
  rows.value.splice(index, 1)
  publish()
  await nextTick()
  const next = rows.value[Math.min(index, rows.value.length - 1)]
  document.getElementById(next ? `${next.key}-name` : `${editorId}-add`)?.focus()
}
async function move(from: number, to: number, keyboard = true) {
  if (props.disabled || from === to || from < 0 || to < 0 || to >= rows.value.length) return
  const row = rows.value.splice(from, 1)[0]
  if (!row) return
  rows.value.splice(to, 0, row)
  publish()
  announcement.value = t('settings.ProfileExtras.fieldMoved', [to + 1])
  if (keyboard) {
    await nextTick()
    document.getElementById(`${row.key}-handle`)?.focus()
  }
}
function dragStart(event: DragEvent, row: Row) {
  if (props.disabled || !event.dataTransfer) { event.preventDefault(); return }
  dragged.value = row.key
  event.dataTransfer.effectAllowed = 'move'
  event.dataTransfer.setData('text/plain', row.key)
}
function drop(event: DragEvent, index: number) {
  if (!dragged.value || props.disabled || event.dataTransfer?.getData('text/plain') !== dragged.value) return
  event.preventDefault()
  void move(rows.value.findIndex(row => row.key === dragged.value), index, false)
  dragged.value = null
}
</script>

<template>
  <details class="profile-fields">
    <summary class="profile-fields__summary">
      <span><t-icon name="view-list" :size="18" aria-hidden="true" />{{ $t('settings.ProfileExtras.extraFields') }}</span>
      <span class="profile-fields__count">{{ filledCount }} / 16 <t-icon name="chevron-down" :size="18" aria-hidden="true" /></span>
    </summary>
    <div class="profile-fields__body">
      <p :id="`${editorId}-hint`" class="profile-fields__hint">{{ $t('settings.ProfileExtras.fieldsHint') }}</p>
      <ol class="profile-fields__list" :aria-describedby="`${editorId}-hint`">
        <li v-for="(row, index) in rows" :key="row.key" class="profile-fields__row" :class="{ 'is-dragging': dragged === row.key }" @dragover="dragged && $event.preventDefault()" @drop="drop($event, index)">
          <button :id="`${row.key}-handle`" type="button" class="profile-fields__icon profile-fields__handle" :draggable="!disabled" :disabled="disabled" :aria-label="$t('settings.ProfileExtras.reorderField', [index + 1])" :title="$t('settings.ProfileExtras.reorderHint')" @dragstart="dragStart($event, row)" @dragend="dragged = null" @keydown.alt.up.prevent="move(index, index - 1)" @keydown.alt.down.prevent="move(index, index + 1)"><t-icon name="menu" :size="18" aria-hidden="true" /></button>
          <label class="profile-fields__name" :for="`${row.key}-name`"><span>{{ $t('settings.ProfileExtras.fieldName') }}</span><input :id="`${row.key}-name`" :value="row.name" :disabled="disabled" type="text" maxlength="255" autocomplete="off" @input="update(row, 'name', $event)" /></label>
          <label class="profile-fields__value" :for="`${row.key}-value`"><span>{{ $t('settings.ProfileExtras.fieldValue') }}</span><input :id="`${row.key}-value`" :value="row.value" :disabled="disabled" type="text" maxlength="2048" autocomplete="off" @input="update(row, 'value', $event)" /></label>
          <div class="profile-fields__controls">
            <button type="button" class="profile-fields__icon" data-action="up" :disabled="disabled || index === 0" :aria-label="$t('settings.ProfileExtras.moveUp')" :title="$t('settings.ProfileExtras.moveUp')" @click="move(index, index - 1)"><t-icon name="chevron-up" :size="18" aria-hidden="true" /></button>
            <button type="button" class="profile-fields__icon" data-action="down" :disabled="disabled || index === rows.length - 1" :aria-label="$t('settings.ProfileExtras.moveDown')" :title="$t('settings.ProfileExtras.moveDown')" @click="move(index, index + 1)"><t-icon name="chevron-down" :size="18" aria-hidden="true" /></button>
            <button type="button" class="profile-fields__icon profile-fields__remove" data-action="remove" :disabled="disabled" :aria-label="$t('settings.ProfileExtras.removeField', [index + 1])" :title="$t('settings.ProfileExtras.removeField', [index + 1])" @click="remove(index)"><t-icon name="close" :size="18" aria-hidden="true" /></button>
          </div>
        </li>
      </ol>
      <button :id="`${editorId}-add`" type="button" class="profile-fields__add" data-testid="profile-fields-add" :disabled="disabled || rows.length >= 16" :title="rows.length >= 16 ? $t('settings.ProfileExtras.fieldsFull') : undefined" @click="add"><t-icon name="add" :size="18" aria-hidden="true" />{{ $t('settings.ProfileExtras.addField') }}</button>
      <p class="profile-fields__hint">{{ $t('settings.ProfileExtras.verificationHint') }}</p>
      <p class="sr-only" role="status" aria-live="polite">{{ announcement }}</p>
    </div>
  </details>
</template>

<style scoped>
.profile-fields { min-width: 0; border: 1px solid var(--color-separator); border-radius: .875rem; background: var(--color-bg-grouped); }
.profile-fields__summary { display: flex; align-items: center; justify-content: space-between; gap: .75rem; padding: 1rem; font-weight: 600; cursor: pointer; list-style: none; }
.profile-fields__summary::-webkit-details-marker { display: none; }
.profile-fields__summary > span { display: inline-flex; align-items: center; gap: .5rem; }
.profile-fields__count { color: var(--color-label-secondary); font-size: .8125rem; font-weight: 400; white-space: nowrap; }
.profile-fields[open] .profile-fields__count :deep(svg) { transform: rotate(180deg); }
.profile-fields__body { display: flex; flex-direction: column; gap: .875rem; border-top: 1px solid var(--color-separator); padding: 1rem; }
.profile-fields__hint { margin: 0; color: var(--color-label-secondary); font-size: .8125rem; line-height: 1.5; }
.profile-fields__list { display: flex; flex-direction: column; gap: .875rem; margin: 0; padding: 0; list-style: none; }
.profile-fields__row { display: grid; grid-template-columns: 30px minmax(0, 1fr) minmax(0, 2fr) auto; gap: .5rem; align-items: end; }
.profile-fields__row.is-dragging { opacity: .45; }
.profile-fields__row label { display: flex; min-width: 0; flex-direction: column; gap: .375rem; font-size: .8125rem; }
.profile-fields__row input { width: 100%; min-width: 0; min-height: 42px; box-sizing: border-box; padding: .5rem .625rem; border: 1px solid var(--color-separator); border-radius: .5rem; background: var(--color-bg-elevated); color: inherit; font: inherit; }
.profile-fields__controls { display: flex; align-items: center; min-height: 42px; }
.profile-fields__icon { display: inline-flex; justify-content: center; align-items: center; width: 30px; height: 36px; flex: none; padding: 0; border: 0; border-radius: .5rem; background: transparent; color: var(--color-label-secondary); cursor: pointer; }
.profile-fields__icon:hover:enabled { background: var(--color-fill); color: var(--color-label); }
.profile-fields__remove:hover:enabled { color: var(--color-danger); }
.profile-fields__handle { height: 42px; cursor: grab; touch-action: pan-y; }
.profile-fields__handle:active { cursor: grabbing; }
/* Full-width bar under the rows, spanning the name + value inputs (between
   the drag handle column and the row controls). */
.profile-fields__add { display: flex; align-items: center; justify-content: center; gap: .375rem; min-height: 42px; margin: 0 calc(90px + .5rem) 0 calc(30px + .5rem); padding: .5rem .75rem; border: 1px dashed var(--color-separator); border-radius: .5rem; background: transparent; color: var(--color-accent); font: inherit; font-size: .875rem; font-weight: 600; cursor: pointer; transition: background-color .15s, border-color .15s; }
.profile-fields__add:hover:enabled { border-color: var(--color-accent); background: color-mix(in srgb, var(--color-accent) 8%, transparent); }
.profile-fields__add:active:enabled { background: color-mix(in srgb, var(--color-accent) 14%, transparent); }
.profile-fields__add:disabled { color: var(--color-label-secondary); border-style: solid; background: var(--color-fill); }
.profile-fields :is(button, input):disabled { opacity: .45; cursor: not-allowed; }
.profile-fields :is(button, input, summary):focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
@media (max-width: 599px) {
  .profile-fields__body { padding: .875rem; }
  .profile-fields__row { grid-template-columns: minmax(0, 1fr) minmax(0, 2fr); padding-bottom: .875rem; border-bottom: 1px solid var(--color-separator); }
  .profile-fields__handle { display: none; }
  .profile-fields__controls { grid-column: 1 / -1; justify-content: flex-end; }
  .profile-fields__icon { width: 40px; height: 40px; }
  .profile-fields__add { margin: 0; min-height: 44px; }
}
</style>
