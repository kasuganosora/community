<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId, watch } from 'vue'
import { safeGroupURL, type Group } from '../../domain/group'
import { groupApi } from '../../infrastructure/groupApi'

import { t } from '@/shared/i18n'
const props = defineProps<{ group: Group }>()
const emit = defineEmits<{ updated: [group: Group] }>()
const id = useId(), input = ref<HTMLInputElement>(), editButton = ref<HTMLButtonElement>()
const editing = ref(false), draft = ref(''), busy = ref(false), copying = ref(false)
const error = ref(''), feedback = ref('')
let generation = 0
const clientURL = computed(() => safeGroupURL(props.group.client_url))
const canEdit = computed(() => !!props.group.capabilities.manage_address && !!props.group.address_server)
const alias = computed(() => draft.value.trim().toLowerCase())
const valid = computed(() => /^[a-z0-9](?:[a-z0-9_-]{0,62}[a-z0-9])?$/.test(alias.value))
const preview = computed(() => valid.value && props.group.address_server ? `https://matrix.to/#/#${alias.value}:${props.group.address_server}` : '')
const invalidNameMessage = t('group.GroupAddressEditor.s20')
const currentName = computed(() => {
  const address = props.group.canonical_alias || '', suffix = `:${props.group.address_server}`
  return address.startsWith('#') && address.endsWith(suffix) ? address.slice(1, -suffix.length) : ''
})

watch(() => props.group.id, () => {
  generation++; editing.value = false; draft.value = ''; busy.value = false; copying.value = false; error.value = ''; feedback.value = ''
})
watch(canEdit, allowed => {
  if (!allowed && editing.value) error.value = t('group.GroupAddressEditor.s21')
})

async function open() {
  draft.value = currentName.value; error.value = ''; feedback.value = ''; editing.value = true
  await nextTick(); input.value?.focus()
}
async function close() {
  if (busy.value) return
  editing.value = false; error.value = ''
  await nextTick(); editButton.value?.focus()
}
function validateDraft() {
  if (alias.value && !valid.value) error.value = invalidNameMessage
}
async function save() {
  if (busy.value || !canEdit.value) return
  error.value = ''; feedback.value = ''
  if (!valid.value) { error.value = invalidNameMessage; input.value?.focus(); return }
  const version = generation, groupID = props.group.id
  busy.value = true
  try {
    const updated = await groupApi.address(groupID, alias.value)
    if (version !== generation) return
    emit('updated', updated); editing.value = false; feedback.value = t('group.GroupAddressEditor.s22')
    await nextTick(); editButton.value?.focus()
  } catch (cause) {
    if (version === generation) error.value = cause instanceof Error ? cause.message : t('group.GroupAddressEditor.s23')
  } finally { if (version === generation) busy.value = false }
}
async function copy() {
  if (copying.value || !clientURL.value) return
  const version = generation
  copying.value = true; error.value = ''; feedback.value = ''
  try {
    await navigator.clipboard.writeText(clientURL.value)
    if (version === generation) feedback.value = t('group.GroupAddressEditor.s24')
  } catch {
    if (version === generation) error.value = t('group.GroupAddressEditor.s25')
  } finally { if (version === generation) copying.value = false }
}
onBeforeUnmount(() => { generation++ })
</script>

<template>
  <section v-if="clientURL || canEdit" class="group-address" :aria-label="$t('group.GroupAddressEditor.s1')">
    <div class="group-address__heading">
      <h3>{{ $t('group.GroupAddressEditor.s1') }}</h3>
      <button v-if="canEdit && !editing" ref="editButton" type="button" class="btn btn--ghost btn--sm" @click="open">{{ group.canonical_alias ? $t('group.GroupAddressEditor.s2') : $t('group.GroupAddressEditor.s3') }}</button>
    </div>
    <form v-if="editing" class="group-address__form" :aria-busy="busy" @submit.prevent="save">
      <label :for="`${id}-name`">{{ $t('group.GroupAddressEditor.s4') }}</label>
      <input :id="`${id}-name`" ref="input" v-model="draft" type="text" :placeholder="$t('group.GroupAddressEditor.s5')" autocomplete="off" autocapitalize="none" spellcheck="false" maxlength="64" :disabled="busy" :aria-invalid="error ? true : undefined" :aria-describedby="`${id}-hint${error ? ` ${id}-error` : ''}`" @input="error = ''" @blur="validateDraft" />
      <p v-if="error" :id="`${id}-error`" class="group-address__error" role="alert">{{ error }}</p>
      <p :id="`${id}-hint`" class="group-address__hint">{{ $t('group.GroupAddressEditor.s6') }}</p>
      <div class="group-address__preview"><span>{{ $t('group.GroupAddressEditor.s7') }}</span><code>{{ preview || $t('group.GroupAddressEditor.s8') }}</code></div>
      <p class="group-address__hint">{{ $t('group.GroupAddressEditor.s11', [group.canonical_alias ? $t('group.GroupAddressEditor.s9') : $t('group.GroupAddressEditor.s10')]) }}</p>
      <div class="group-address__actions">
        <button type="submit" class="btn btn--primary btn--sm" :disabled="busy || !canEdit || !alias || alias === currentName">{{ busy ? $t('group.GroupAddressEditor.s12') : $t('group.GroupAddressEditor.s13') }}</button>
        <button type="button" class="btn btn--secondary btn--sm" :disabled="busy" @click="close">{{ $t('group.GroupAddressEditor.s14') }}</button>
      </div>
    </form>
    <template v-else>
      <p v-if="!group.canonical_alias && canEdit" class="group-address__hint">{{ $t('group.GroupAddressEditor.s15') }}</p>
      <div v-if="clientURL" class="group-address__share">
        <a :href="clientURL" target="_blank" rel="noopener noreferrer" :aria-label="group.canonical_alias ? $t('group.GroupAddressEditor.s16', [group.canonical_alias]) : undefined">{{ group.canonical_alias || $t('group.GroupAddressEditor.s17') }} ↗</a>
        <button type="button" class="btn btn--secondary btn--sm" :disabled="copying" @click="copy">{{ copying ? $t('group.GroupAddressEditor.s18') : $t('group.GroupAddressEditor.s19') }}</button>
      </div>
    </template>
    <p v-if="error && !editing" :id="`${id}-error`" class="group-address__error" role="alert">{{ error }}</p>
    <p v-if="feedback" class="group-address__feedback" role="status">{{ feedback }}</p>
  </section>
</template>

<style scoped>
.group-address { display: grid; gap: .75rem; min-width: 0; margin-top: 1rem; padding-top: 1rem; border-top: 1px solid var(--color-separator); }
.group-address__heading { display: flex; align-items: center; justify-content: space-between; gap: .5rem; }
.group-address h3 { margin: 0; font-size: .9rem; }
.group-address__form { display: grid; gap: .5rem; min-width: 0; }
.group-address__form label { font-size: .8rem; font-weight: 550; }
.group-address__form input { width: 100%; min-width: 0; min-height: 44px; padding: .6rem .7rem; border: 1px solid var(--color-separator); border-radius: 9px; background: var(--color-bg-row); color: var(--color-label); font: inherit; font-size: 1rem; }
.group-address__form input[aria-invalid="true"] { border-color: var(--color-danger); }
.group-address__hint { margin: 0; color: var(--color-label-secondary); font-size: .75rem; line-height: 1.55; }
.group-address__preview { display: grid; gap: .25rem; padding: .65rem .75rem; border-radius: 9px; background: var(--color-fill); font-size: .75rem; }
.group-address__preview span { color: var(--color-label-secondary); }
.group-address__preview code { padding: 0; border: 0; background: transparent; font: inherit; overflow-wrap: anywhere; user-select: text; }
.group-address__actions { display: flex; flex-wrap: wrap; gap: .5rem; margin-top: .3rem; }
.group-address__share { display: flex; flex-wrap: wrap; gap: .6rem; align-items: center; }
.group-address__share a { flex: 1 1 150px; min-width: 0; color: var(--color-accent-text); overflow-wrap: anywhere; font-size: .8rem; }
.group-address__share button { flex-shrink: 0; }
.group-address__error, .group-address__feedback { margin: 0; font-size: .8rem; line-height: 1.5; overflow-wrap: anywhere; }
.group-address__error { color: var(--color-danger); }
.group-address__feedback { color: var(--color-accent-text); }
.group-address :is(button, input, a):focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
.group-address button:disabled, .group-address input:disabled { opacity: .5; }
@media (pointer: coarse) { .group-address button, .group-address input, .group-address__share a { min-height: 44px; } .group-address input { font-size: 1rem; } .group-address__share a { display: flex; align-items: center; } }
</style>
