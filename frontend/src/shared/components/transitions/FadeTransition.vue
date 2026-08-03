<script setup lang="ts">
interface Props {
  mode?: 'out-in' | 'in-out' | 'default'
  duration?: number
}

withDefaults(defineProps<Props>(), {
  mode: 'default',
  duration: 300,
})
</script>

<template>
  <Transition
    name="fade"
    :mode="mode"
    :style="{ '--transition-duration': `${duration}ms` }"
  >
    <slot />
  </Transition>
</template>

<style>
.fade-enter-active,
.fade-leave-active {
  transition: opacity var(--transition-duration, 300ms) ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
