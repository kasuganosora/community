<script setup lang="ts">
defineProps<{ title: string; failed?: boolean }>()
function reload() { window.location.reload() }
</script>

<template>
  <section class="route-pending">
    <div class="page-header"><h1 class="page-header__title">{{ title }}</h1></div>
    <div v-if="failed" class="empty-state" role="alert">
      <div class="empty-state__icon"><t-icon name="error-circle" :size="48" /></div>
      <p class="empty-state__text">{{ $t('shared.RoutePending.s1') }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="reload">{{ $t('shared.RoutePending.s2') }}</button>
    </div>
    <div v-else class="page-loading" role="status" :aria-label="$t('shared.RoutePending.s3', [title])">
      <span class="loading-spinner" aria-hidden="true" />
    </div>
  </section>
</template>

<style scoped>
.route-pending { min-height: 12rem; }
</style>
