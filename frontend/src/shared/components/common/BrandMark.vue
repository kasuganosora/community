<script setup lang="ts">
import { ref, watch } from 'vue'
import LogoIcon from './LogoIcon.vue'
import { useBranding } from '@/shared/composables/useBranding'

defineProps<{ size?: number }>()
const { branding } = useBranding()
const broken = ref(false)
watch(() => branding.value.iconUrl, () => { broken.value = false })
</script>

<template>
  <img
    v-if="branding.iconUrl && !broken"
    class="brand-mark"
    :src="branding.iconUrl"
    alt=""
    :width="size || 28"
    :height="size || 28"
    decoding="async"
    referrerpolicy="no-referrer"
    @error="broken = true"
  />
  <LogoIcon v-else :size="size" />
</template>

<style scoped>
.brand-mark {
  display: block;
  flex: none;
  object-fit: cover;
  border-radius: 22%;
}
</style>
