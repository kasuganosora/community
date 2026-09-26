<script setup lang="ts">
/**
 * Drop-in replacement for TDesign's <t-icon>, wired in through the
 * unplugin-vue-components resolver in vite.config.ts so templates keep
 * using <t-icon name="..." :size="18" />.
 *
 * Why: <t-icon> renders an SVG sprite that it downloads at runtime from
 * tdesign.gtimg.com. The backend's Content-Security-Policy blocks that
 * script, so every icon in the served app was blank. It also declares
 * `size` as a String, so the numeric sizes used everywhere flooded the
 * console with Vue prop warnings. This component renders the icon as an
 * inline SVG component bundled from tdesign-icons-vue-next instead.
 */
import { computed } from 'vue'
import { resolveIcon, normalizeIconSize } from './appIcons'

const props = defineProps<{
  name: string
  size?: number | string
}>()

const component = computed(() => resolveIcon(props.name))
const fontSize = computed(() => normalizeIconSize(props.size))

if (import.meta.env.DEV && !component.value) {
  console.warn(`[AppIcon] "${props.name}" is not registered in appIcons.ts`)
}
</script>

<template>
  <component
    :is="component"
    v-if="component"
    :size="fontSize"
    aria-hidden="true"
  />
</template>
