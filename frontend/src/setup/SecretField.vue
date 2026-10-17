<script setup lang="ts">
import { ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

defineProps<{ label: string; hint?: string; autocomplete?: string; required?: boolean; minlength?: number; field?: string; invalid?: boolean }>()
const value = defineModel<string>({ required: true })
const visible = ref(false)
const id = useId()
const { t } = useI18n()
</script>

<template>
  <div class="field">
    <label :for="id">{{ label }}</label>
    <div class="secret-field">
      <input :id="id" v-model="value" :type="visible ? 'text' : 'password'" :autocomplete="autocomplete || 'off'" :required="required" :minlength="minlength" :data-field="field" :aria-invalid="invalid" :aria-describedby="invalid ? 'setup-error' : hint ? `${id}-hint` : undefined" autocapitalize="off" spellcheck="false">
      <button type="button" :aria-label="`${t(visible ? 'common.hide' : 'common.show')} · ${label}`" :aria-pressed="visible" @click="visible = !visible">{{ t(visible ? 'common.hide' : 'common.show') }}</button>
    </div>
    <small v-if="hint" :id="`${id}-hint`">{{ hint }}</small>
  </div>
</template>
