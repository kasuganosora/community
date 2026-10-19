<script setup lang="ts">
import { onErrorCaptured, ref } from 'vue'

/** Keeps a crash inside one settings section from unmounting its siblings. */
const failed = ref(false), attempt = ref(0)
onErrorCaptured(err => {
  console.error('[settings section]', err)
  failed.value = true
  return false
})
function retry() { failed.value = false; attempt.value++ }
</script>

<template>
  <section v-if="failed" class="connection-panel"><div class="connection-body" style="padding-top: 20px"><p class="connection-error" role="alert">{{ $t('settings.Connections.sectionError') }} <button class="btn btn--ghost" @click="retry">{{ $t('settings.Connections.retry') }}</button></p></div></section>
  <div v-else :key="attempt" class="settings-section-boundary"><slot /></div>
</template>

<style>
.settings-section-boundary { display: contents; }
</style>
