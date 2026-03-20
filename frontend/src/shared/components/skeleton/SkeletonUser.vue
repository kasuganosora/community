<script setup lang="ts">
import SkeletonBase from './SkeletonBase.vue'

interface Props {
  count?: number
}

withDefaults(defineProps<Props>(), {
  count: 3,
})
</script>

<template>
  <div v-for="i in count" :key="i" class="skeleton-user">
    <SkeletonBase width="48" height="48" circle />
    <div class="skeleton-user__info">
      <SkeletonBase width="120" height="16" />
      <SkeletonBase width="100" height="14" />
    </div>
    <SkeletonBase width="80" height="32" />
  </div>
</template>

<style scoped>
.skeleton-user {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--border-color);
}

.skeleton-user__info {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  flex: 1;
}
</style>
