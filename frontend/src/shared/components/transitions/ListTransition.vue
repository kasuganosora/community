<script setup lang="ts">
import { computed } from 'vue'

interface Props {
  tag?: string
  name?: string
  duration?: number
  stagger?: boolean
  staggerDelay?: number
}

const props = withDefaults(defineProps<Props>(), {
  tag: 'div',
  name: 'list',
  duration: 300,
  stagger: false,
  staggerDelay: 50,
})

// When stagger is enabled, append '-stagger' to the name so the
// list-stagger-* transition classes are applied; otherwise use the
// plain name so the list-* classes apply.
const transitionName = computed(() =>
  props.stagger ? `${props.name}-stagger` : props.name,
)

</script>


<template>
  <TransitionGroup
    :tag="tag"
    :name="transitionName"
    :style="{
      '--transition-duration': `${duration}ms`,
      '--stagger-delay': `${staggerDelay}ms`,
    }"
  >
    <slot />
  </TransitionGroup>
</template>


<style>
/* List transitions */
.list-move,
.list-enter-active,
.list-leave-active {
  transition: all var(--transition-duration, 300ms) ease;
}

.list-enter-from {
  opacity: 0;
  transform: translateX(-30px);
}

.list-leave-to {
  opacity: 0;
  transform: translateX(30px);
}

.list-leave-active {
  position: absolute;
}

/* Staggered list */
.list-stagger-move,
.list-stagger-enter-active,
.list-stagger-leave-active {
  transition: all var(--transition-duration, 300ms) ease;
  transition-delay: calc(var(--index, 0) * var(--stagger-delay, 50ms));
}

.list-stagger-enter-from {
  opacity: 0;
  transform: translateY(20px);
}

.list-stagger-leave-to {
  opacity: 0;
  transform: translateY(-20px);
}

.list-stagger-leave-active {
  position: absolute;
}
</style>
