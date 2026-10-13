<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { groupApi } from '../../infrastructure/groupApi'
import type { GroupInvitee } from '../../domain/group'
import GroupAvatar from './GroupAvatar.vue'

const props = withDefaults(defineProps<{
  modelValue: string; label?: string; multiple?: boolean; disabled?: boolean; required?: boolean; exclude?: string[]
}>(), { label: '邀请成员', exclude: () => [] })
const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
const id = useId(), root = ref<HTMLElement>(), input = ref<HTMLInputElement>()
const focused = ref(false), opened = ref(false), loading = ref(false), composing = ref(false)
const caret = ref(0), active = ref(-1), results = ref<GroupInvitee[]>([]), error = ref('')
let timer: ReturnType<typeof setTimeout> | undefined, controller: AbortController | undefined, generation = 0
let dismissedValue: string | null = null
const token = computed(() => {
  const value = props.modelValue
  let start = 0, end = value.length
  if (props.multiple) {
    start = end = Math.min(caret.value, value.length)
    while (start > 0 && !/[\s,，]/.test(value[start - 1]!)) start--
    while (end < value.length && !/[\s,，]/.test(value[end]!)) end++
  }
  return { start, end, text: value.slice(start, end).trim() }
})
const candidates = computed(() => {
  const excluded = new Set(props.exclude)
  const rest = props.multiple ? props.modelValue.slice(0, token.value.start) + ' ' + props.modelValue.slice(token.value.end) : ''
  const selected = new Set(rest.split(/[\s,，]+/).map(value => value.replace(/^@/, '').toLowerCase()))
  return results.value.filter(person => !excluded.has(person.user_id) && !selected.has(person.username.toLowerCase()) && !selected.has(person.user_id.slice(1).toLowerCase()))
})
const status = computed(() => loading.value ? '正在查找本站账号…' : error.value || (!candidates.value.length ? '未找到可邀请的本站账号；外部 Matrix 账号可直接输入。' : ''))

function cancelSearch() { generation++; clearTimeout(timer); controller?.abort(); loading.value = false }
function close() { cancelSearch(); opened.value = false; active.value = -1; results.value = []; dismissedValue = props.modelValue }
function search() {
  cancelSearch(); results.value = []; active.value = -1; error.value = ''; opened.value = false
  const query = token.value.text
  if (!focused.value || props.disabled || composing.value || dismissedValue === props.modelValue || !query || query === '@') return
  opened.value = true; loading.value = true
  const version = generation
  timer = setTimeout(async () => {
    controller = new AbortController()
    try {
      const users = await groupApi.invitees(query, controller.signal)
      if (version !== generation) return
      results.value = users; active.value = candidates.value.length ? 0 : -1
    } catch {
      if (version === generation) error.value = '账号提示暂时不可用，可继续输入完整账号并邀请。'
    } finally { if (version === generation) loading.value = false }
  }, 250)
}
watch([() => token.value.text, focused, () => props.disabled, composing], search)
watch(sessionEpoch, close, { flush: 'sync' })
onBeforeUnmount(cancelSearch)
function update(event: Event) {
  const target = event.target as HTMLInputElement
  dismissedValue = null; caret.value = target.selectionStart ?? target.value.length
  emit('update:modelValue', target.value)
}
function focus() { dismissedValue = null; focused.value = true; caret.value = input.value?.selectionStart ?? props.modelValue.length }
function blur(event: FocusEvent) { if (!root.value?.contains(event.relatedTarget as Node | null)) { focused.value = false; close() } }
async function choose(person: GroupInvitee) {
  if (props.disabled) return
  const { start, end } = token.value
  const replacement = '@' + person.username
  const tail = props.modelValue.slice(end)
  const separator = props.multiple && !tail ? ' ' : ''
  const value = props.modelValue.slice(0, start) + replacement + separator + tail
  close(); dismissedValue = value
  emit('update:modelValue', value)
  await nextTick()
  input.value?.focus()
  // focus() may clear the dismissal when selection came from a touch tap.
  dismissedValue = value
  caret.value = start + replacement.length + separator.length
  input.value?.setSelectionRange(caret.value, caret.value)
}
async function keydown(event: KeyboardEvent) {
  if (event.isComposing || composing.value || event.keyCode === 229) return
  if (event.key === 'Escape' && opened.value) { event.preventDefault(); event.stopPropagation(); close(); return }
  if (event.key === 'Tab') { close(); return }
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    if (!opened.value) { dismissedValue = null; search() }
    if (!candidates.value.length) return
    event.preventDefault()
    active.value = (active.value + (event.key === 'ArrowDown' ? 1 : -1) + candidates.value.length) % candidates.value.length
    await nextTick()
    document.getElementById(`${id}-option-${active.value}`)?.scrollIntoView?.({ block: 'nearest' })
  } else if (event.key === 'Enter' && opened.value && candidates.value[active.value]) {
    event.preventDefault(); void choose(candidates.value[active.value]!)
  }
}
function paste(event: ClipboardEvent) {
  if (!props.multiple) return
  const text = event.clipboardData?.getData('text')
  if (!text || !/[\r\n]/.test(text)) return
  event.preventDefault()
  input.value?.setRangeText(text.replace(/[\r\n]+/g, ' '), input.value.selectionStart ?? 0, input.value.selectionEnd ?? 0, 'end')
  if (input.value) update({ target: input.value } as unknown as Event)
}
</script>

<template>
  <div ref="root" class="group-member-input" @focusout="blur">
    <label :for="id">{{ label }}</label>
    <input :id="id" ref="input" :value="modelValue" :disabled="disabled" :required="required" :maxlength="multiple ? 4000 : 500"
      role="combobox" aria-autocomplete="list" :aria-expanded="opened" :aria-controls="opened ? `${id}-options` : undefined"
      :aria-activedescendant="opened && candidates[active] ? `${id}-option-${active}` : undefined" :aria-describedby="`${id}-help`"
      autocomplete="off" autocapitalize="none" :spellcheck="false" placeholder="搜索本站用户名、昵称，或输入 Matrix 账号"
      @input="update" @focus="focus" @click="caret = input?.selectionStart ?? modelValue.length" @select="caret = input?.selectionStart ?? modelValue.length"
      @keydown="keydown" @paste="paste" @compositionstart="composing = true" @compositionend="composing = false; update($event)"
/>
    <div v-if="opened" class="group-member-input__suggestions">
      <p class="group-member-input__heading">本站账号</p>
      <ul :id="`${id}-options`" role="listbox" aria-label="本站账号" :aria-busy="loading">
        <li v-for="(person, index) in candidates" :id="`${id}-option-${index}`" :key="person.user_id" role="option" tabindex="-1"
          :aria-selected="active === index" @mousedown.prevent @click="choose(person)"
>
          <GroupAvatar :name="person.display_name || person.username" :url="person.avatar_url" :size="36" />
          <span class="group-member-input__identity"><strong>{{ person.display_name || person.username }}</strong><small>@{{ person.username }}</small></span>
        </li>
      </ul>
      <p v-if="status" class="group-member-input__status" role="status">{{ status }}</p>
    </div>
    <p :id="`${id}-help`" class="group-member-input__help">{{ multiple ? '可连续选择多个本站账号，或用空格分隔完整 Matrix 账号。' : '选择本站账号，或输入 @用户名:服务器；仅支持 Community 和 Matrix。' }}</p>
  </div>
</template>

<style scoped>
.group-member-input { display: grid; gap: .4rem; min-width: 0; }
.group-member-input > label { font-size: .85rem; font-weight: 550; }
.group-member-input > input { width: 100%; min-width: 0; min-height: 44px; border: 1px solid var(--color-separator); border-radius: 9px; padding: .65rem .75rem; background: var(--color-bg-row); color: var(--color-label); font: inherit; font-size: max(1rem, 16px); }
.group-member-input > input:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
.group-member-input__suggestions { min-width: 0; overflow: hidden; border: 1px solid var(--color-separator); border-radius: 12px; background: var(--color-bg-elevated, var(--color-bg)); }
.group-member-input__heading { margin: 0; padding: .65rem .75rem .4rem; color: var(--color-label-secondary); font-size: .75rem; }
.group-member-input ul { list-style: none; padding: .25rem; margin: 0; max-height: 16rem; overflow-y: auto; overscroll-behavior: contain; }
.group-member-input li { display: flex; align-items: center; gap: .65rem; min-height: 56px; padding: .5rem; border-radius: 8px; cursor: pointer; }
.group-member-input li[aria-selected="true"] { background: var(--color-accent-soft); }
@media (hover: hover) { .group-member-input li:hover { background: var(--color-fill); } }
.group-member-input li:active { background: var(--color-pressed); }
.group-member-input__identity { min-width: 0; flex: 1; text-align: left; }
.group-member-input__identity strong, .group-member-input__identity small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.group-member-input__identity strong { color: var(--color-label); font-size: .875rem; font-weight: 600; }
.group-member-input__identity small { color: var(--color-label-secondary); font-size: .8rem; }
.group-member-input__help, .group-member-input__status { margin: 0; color: var(--color-label-secondary); font-size: .75rem; line-height: 1.6; overflow-wrap: anywhere; }
.group-member-input__status { padding: .25rem .75rem .75rem; }
</style>
