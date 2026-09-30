<script setup lang="ts">
interface Props {
  direction?: 'up' | 'down' | 'left' | 'right'
  mode?: 'out-in' | 'in-out' | 'default'
  duration?: number
  distance?: string
}

withDefaults(defineProps<Props>(), {
  direction: 'up',
  mode: 'default',
  duration: 200,
  distance: '8px',
})
</script>

<template>
  <Transition
    :name="`slide-${direction}`"
    :mode="mode"
    :style="{
      '--transition-duration': `${duration}ms`,
      '--slide-distance': distance,
    }"
  >
    <slot />
  </Transition>
</template>

<style>
.slide-up-enter-active,
.slide-up-leave-active,
.slide-down-enter-active,
.slide-down-leave-active,
.slide-left-enter-active,
.slide-left-leave-active,
.slide-right-enter-active,
.slide-right-leave-active {
  transition:
    opacity var(--transition-duration, 200ms) var(--ease-out),
    transform var(--transition-duration, 200ms) var(--ease-out);
}

/* Enter and leave along the same path. A surface that arrives from below
   dismisses back down — it does not continue out the opposite side. */
.slide-up-enter-from,
.slide-up-leave-to {
  opacity: 0;
  transform: translateY(var(--slide-distance, 20px));
}

.slide-down-enter-from,
.slide-down-leave-to {
  opacity: 0;
  transform: translateY(calc(var(--slide-distance, 20px) * -1));
}

.slide-left-enter-from,
.slide-left-leave-to {
  opacity: 0;
  transform: translateX(var(--slide-distance, 20px));
}

.slide-right-enter-from,
.slide-right-leave-to {
  opacity: 0;
  transform: translateX(calc(var(--slide-distance, 20px) * -1));
}

@media (prefers-reduced-motion: reduce) {
  .slide-up-enter-from,
  .slide-up-leave-to,
  .slide-down-enter-from,
  .slide-down-leave-to,
  .slide-left-enter-from,
  .slide-left-leave-to,
  .slide-right-enter-from,
  .slide-right-leave-to {
    transform: none;
  }
}
</style>
