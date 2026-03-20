<script setup lang="ts">
import SkeletonBase from './SkeletonBase.vue'

interface Props {
  count?: number
}

withDefaults(defineProps<Props>(), {
  count: 1,
})
</script>

<template>
  <div v-for="i in count" :key="i" class="skeleton-post">
    <div class="skeleton-post__header">
      <SkeletonBase width="40" height="40" circle />
      <div class="skeleton-post__meta">
        <SkeletonBase width="120" height="16" />
        <SkeletonBase width="80" height="14" />
      </div>
    </div>
    <div class="skeleton-post__content">
      <SkeletonBase width="100%" height="16" />
      <SkeletonBase width="90%" height="16" />
      <SkeletonBase width="60%" height="16" />
    </div>
    <div class="skeleton-post__actions">
      <SkeletonBase width="60" height="20" />
      <SkeletonBase width="60" height="20" />
      <SkeletonBase width="60" height="20" />
      <SkeletonBase width="60" height="20" />
    </div>
  </div>
</template>

<style scoped>
.skeleton-post {
  padding: var(--space-4);
  border-bottom: 1px solid var(--border-color);
}

.skeleton-post__header {
  display: flex;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}

.skeleton-post__meta {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  flex: 1;
}

.skeleton-post__content {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}

.skeleton-post__actions {
  display: flex;
  justify-content: space-between;
  max-width: 425px;
}
</style>
