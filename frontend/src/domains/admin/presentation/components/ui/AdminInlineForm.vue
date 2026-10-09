<script setup lang="ts">
defineOptions({ inheritAttrs: false })
import { useId } from 'vue'
withDefaults(defineProps<{
  label: string
  buttonLabel: string
  placeholder?: string
  type?: string
  busy?: boolean
  disabled?: boolean
  required?: boolean
}>(), { type: 'text', required: true })
const model = defineModel<string>({ default: '' })
const id = useId()
const emit = defineEmits<{ submit: [] }>()
</script>

<template>
  <form class="admin-inline-form" @submit.prevent="emit('submit')">
    <label class="admin-field__label" :for="id">{{ label }}</label>
    <div class="admin-inline-form__row">
      <input v-bind="$attrs" :id="id" v-model="model" :type="type" :required="required" :placeholder="placeholder" :disabled="busy || disabled" />
      <button type="submit" class="admin-button admin-button--primary" :disabled="busy || disabled" :aria-busy="busy || undefined">
        <span v-if="busy" class="admin-spinner" aria-hidden="true" />{{ buttonLabel }}
      </button>
    </div>
  </form>
</template>
