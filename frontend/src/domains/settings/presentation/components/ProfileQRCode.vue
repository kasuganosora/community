<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { sessionEpoch } from '@/shared/api/sessionScope'

const props = defineProps<{ account: MastodonAccount }>()
const visible = ref(false)
const busy = ref(false)
const failed = ref(false)
const image = ref('')
const svg = ref('')
const dialog = ref<HTMLDialogElement>()
const trigger = ref<HTMLButtonElement>()
let generation = 0
const profileURL = computed(() => {
  const fallback = `${window.location.origin}/@${encodeURIComponent(props.account.username)}`
  try {
    const url = new URL(props.account.url || fallback, window.location.origin)
    return ['https:', 'http:'].includes(url.protocol) && !url.username && !url.password ? url.href : fallback
  } catch { return fallback }
})

async function generate() {
  const current = ++generation
  busy.value = true
  failed.value = false
  try {
    const { default: createQRCode } = await import('qrcode-generator')
    if (current !== generation) return
    const code = createQRCode(0, 'M')
    code.addData(profileURL.value)
    code.make()
    image.value = code.createDataURL(6, 24)
    svg.value = code.createSvgTag({ cellSize: 6, margin: 24, scalable: true })
  } catch { if (current === generation) failed.value = true }
  finally { if (current === generation) busy.value = false }
}

async function open() {
  visible.value = true
  await nextTick()
  dialog.value?.showModal()
  void generate()
}
function close(restoreFocus = true) {
  generation++
  dialog.value?.close()
  visible.value = false
  busy.value = false
  image.value = ''
  svg.value = ''
  if (restoreFocus) void nextTick(() => trigger.value?.focus())
}
function download() {
  if (!svg.value) return
  const url = URL.createObjectURL(new Blob([svg.value], { type: 'image/svg+xml' }))
  const link = document.createElement('a')
  link.href = url
  link.download = `${props.account.username}-profile.svg`
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
watch([() => props.account.id, profileURL, sessionEpoch], () => close(false))
onBeforeUnmount(() => close(false))
</script>

<template>
  <button ref="trigger" type="button" class="profile-qr-trigger" @click="open">
    <span><t-icon name="qrcode" size="20" aria-hidden="true" />{{ $t('settings.ProfileQRCode.open') }}</span>
    <t-icon name="chevron-right" size="18" aria-hidden="true" />
  </button>
  <Teleport to="body">
    <dialog v-if="visible" ref="dialog" class="profile-qr-dialog" aria-labelledby="profile-qr-title" @cancel.prevent="close()" @click.self="close()">
      <header><h2 id="profile-qr-title">{{ $t('settings.ProfileQRCode.title') }}</h2><button type="button" class="btn btn--ghost" :aria-label="$t('settings.ProfileQRCode.close')" autofocus @click="close()"><t-icon name="close" size="20" /></button></header>
      <div class="profile-qr-content">
        <p v-if="busy" role="status">{{ $t('settings.ProfileQRCode.creating') }}</p>
        <div v-else-if="failed" role="alert"><p>{{ $t('settings.ProfileQRCode.error') }}</p><button class="btn btn--secondary" @click="generate">{{ $t('settings.ProfileQRCode.retry') }}</button></div>
        <img v-else-if="image" :src="image" :alt="$t('settings.ProfileQRCode.title')" />
        <strong>{{ account.display_name || account.username }}</strong>
        <a :href="profileURL" target="_blank" rel="noopener noreferrer">{{ profileURL }}</a>
        <button type="button" class="btn btn--primary" :disabled="busy || !image" @click="download">{{ $t('settings.ProfileQRCode.download') }}</button>
      </div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.profile-qr-trigger { display: flex; align-items: center; justify-content: space-between; width: 100%; gap: 1rem; padding: 1rem; margin-top: 1.5rem; border: 1px solid var(--color-separator); border-radius: 12px; color: var(--color-label); background: var(--color-bg-grouped); font: inherit; cursor: pointer; }
.profile-qr-trigger > span { display: flex; align-items: center; gap: .75rem; }
.profile-qr-dialog { width: min(420px, calc(100vw - 32px)); max-height: calc(100dvh - 32px); margin: auto; padding: 0; border: 1px solid var(--color-separator); border-radius: 18px; color: var(--color-label); background: var(--color-bg-elevated); }
.profile-qr-dialog::backdrop { background: rgb(0 0 0 / 60%); }
.profile-qr-dialog header { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: .75rem 1rem; border-bottom: 1px solid var(--color-separator); }
.profile-qr-dialog h2 { margin: 0; font-size: 1rem; }
.profile-qr-content { display: flex; flex-direction: column; align-items: center; gap: 1rem; padding: 1.5rem; overflow: auto; text-align: center; }
.profile-qr-content img { display: block; width: 256px; max-width: 100%; height: auto; background: white; border-radius: 8px; image-rendering: pixelated; }
.profile-qr-content a { overflow-wrap: anywhere; max-width: 100%; font-size: .85rem; }
</style>
