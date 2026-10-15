<script setup lang="ts">
/**
 * <t-dialog> (mapped in vite.config.ts): renders TDesign's Dialog, but only
 * loads it the first time the dialog opens. Dialogs sit inside post cards
 * and the app layout, so a static import put TDesign's Dialog, Button and
 * shared runtime on every first screen. Props, listeners and slots pass
 * through unchanged.
 */
import { defineAsyncComponent, ref, watch } from 'vue'

// Named like TDesign's component so tests can stub it as 't-dialog'.
defineOptions({ name: 'TDialog', inheritAttrs: false })
const props = defineProps<{ visible?: boolean }>()
const Dialog = defineAsyncComponent(() => import('@/shared/ui/tdesignDialog'))
const opened = ref(!!props.visible)
watch(() => props.visible, (v) => { if (v) opened.value = true })
</script>

<template>
  <component :is="Dialog" v-if="opened" v-bind="$attrs" :visible="visible">
    <template v-for="(_, name) in $slots" #[name]="scope">
      <slot :name="name" v-bind="scope || {}" />
    </template>
  </component>
</template>
