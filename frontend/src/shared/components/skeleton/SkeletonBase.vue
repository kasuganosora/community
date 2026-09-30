<script setup lang="ts">
interface Props {
  width?: string | number
  height?: string | number
  circle?: boolean
  animated?: boolean
}

withDefaults(defineProps<Props>(), {
  width: '100%',
  height: '1em',
  circle: false,
  animated: true,
})

function getStyle(value: string | number): string {
  if (typeof value === 'number') return `${value}px`
  return value
}
</script>

<template>
  <div
    class="skeleton-base"
    :class="{ 'skeleton-animated': animated, 'skeleton-circle': circle }"
    :style="{
      width: getStyle(width),
      height: getStyle(height),
    }"
  />
</template>

<style scoped>
.skeleton-base {
  background: var(--bg-hover);
  border-radius: var(--radius-sm);
  display: inline-block;
}

.skeleton-circle {
  border-radius: 50%;
}

.skeleton-animated {
  background: linear-gradient(
    90deg,
    var(--bg-hover) 25%,
    var(--bg-elevated) 50%,
    var(--bg-hover) 75%
  );
  background-size: 200% 100%;
  animation: skeleton-loading 1.5s ease-in-out infinite;
}

@keyframes skeleton-loading {
  0% {
    background-position: 200% 0;
  }
  100% {
    background-position: -200% 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .skeleton-animated {
    animation: none;
    background: var(--bg-hover);
  }
}
</style>
