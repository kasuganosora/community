<script setup lang="ts">
withDefaults(defineProps<{
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost'
  size?: 'md' | 'sm'
  loading?: boolean
  disabled?: boolean
  type?: 'button' | 'submit' | 'reset'
}>(), { variant: 'secondary', size: 'md', loading: false, disabled: false, type: 'button' })
</script>

<template>
  <button
    :type="type"
    class="admin-button"
    :class="[variant !== 'secondary' && `admin-button--${variant}`, size === 'sm' && 'admin-button--sm', loading && 'admin-button--loading']"
    :disabled="disabled || loading"
    :aria-busy="loading || undefined"
  >
    <span v-if="loading" class="admin-spinner" aria-hidden="true" />
    <slot />
  </button>
</template>
