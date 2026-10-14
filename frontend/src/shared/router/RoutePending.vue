<script setup lang="ts">
defineProps<{ title: string; failed?: boolean }>()
function reload() { window.location.reload() }
</script>

<template>
  <section class="route-pending">
    <div class="page-header"><h1 class="page-header__title">{{ title }}</h1></div>
    <div v-if="failed" class="empty-state" role="alert">
      <p class="empty-state__text">页面加载失败，请重试</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="reload">重新加载</button>
    </div>
    <div v-else class="page-loading" role="status" :aria-label="`正在加载${title}`">
      <span class="loading-spinner" aria-hidden="true" />
    </div>
  </section>
</template>

<style scoped>
.route-pending { min-height: 12rem; }
</style>
