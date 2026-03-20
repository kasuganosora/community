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
  duration: 300,
  distance: '20px',
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

<style scoped>
.slide-up-enter-active,
.slide-up-leave-active,
.slide-down-enter-active,
.slide-down-leave-active,
.slide-left-enter-active,
.slide-left-leave-active,
.slide-right-enter-active,
.slide-right-leave-active {
  transition: all var(--transition-duration, 300ms) ease;
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
