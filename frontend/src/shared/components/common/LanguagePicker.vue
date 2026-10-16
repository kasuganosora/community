<script setup lang="ts">
import { LOCALES, currentLocale, isLocale } from '@/shared/i18n'
import { chooseUiLocale } from '@/shared/composables/useUiLocale'
import { AUTH_TOKEN_KEY } from '@/shared/api/client'

defineProps<{ compact?: boolean }>()
const value = currentLocale()

// Read the token directly so the picker also works outside the app's Pinia (guest pages, tests).
function signedIn(): boolean {
  try { return !!localStorage.getItem(AUTH_TOKEN_KEY) } catch { return false }
}

function onChange(e: Event) {
  const v = (e.target as HTMLSelectElement).value
  if (isLocale(v)) void chooseUiLocale(v, signedIn())
}
</script>

<template>
  <label class="lang-picker" :class="{ 'lang-picker--compact': compact }">
    <t-icon name="earth" :size="16" aria-hidden="true" />
    <span class="lang-picker__label" :class="{ 'sr-only': compact }">{{ $t('shared.LanguagePicker.label') }}</span>
    <select class="lang-picker__select" :value="value" :aria-label="$t('shared.LanguagePicker.label')" @change="onChange">
      <option v-for="l in LOCALES" :key="l.code" :value="l.code" :lang="l.code">{{ l.name }}</option>
    </select>
  </label>
</template>

<style scoped>
.lang-picker { display: inline-flex; align-items: center; gap: var(--space-2, 8px); color: var(--color-label-secondary); font-size: 14px; max-width: 100%; }
.lang-picker__label { white-space: nowrap; }
.lang-picker__select {
  min-height: 36px; padding: 0 28px 0 10px; border-radius: 8px; max-width: 100%;
  border: 1px solid var(--color-separator); background: var(--color-bg-row);
  color: var(--color-label); font: inherit; cursor: pointer;
}
.lang-picker__select:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 1px; }
.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0,0,0,0); white-space: nowrap; border: 0; }
</style>
