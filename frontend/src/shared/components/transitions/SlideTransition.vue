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

/* Slide Up */
.slide-up-enter-from {
  opacity: 0;
  transform: translateY(var(--slide-distance, 20px));
}
.slide-up-leave-to {
  opacity: 0;
  transform: translateY(calc(var(--slide-distance, 20px) * -1));
}

/* Slide Down */
.slide-down-enter-from {
  opacity: 0;
  transform: translateY(calc(var(--slide-distance, 20px) * -1));
}
.slide-down-leave-to {
  opacity: 0;
  transform: translateY(var(--slide-distance, 20px));
}

/* Slide Left */
.slide-left-enter-from {
  opacity: 0;
  transform: translateX(var(--slide-distance, 20px));
}
.slide-left-leave-to {
  opacity: 0;
  transform: translateX(calc(var(--slide-distance, 20px) * -1));
}

/* Slide Right */
.slide-right-enter-from {
  opacity: 0;
  transform: translateX(calc(var(--slide-distance, 20px) * -1));
}
.slide-right-leave-to {
  opacity: 0;
  transform: translateX(var(--slide-distance, 20px));
}
</style>
