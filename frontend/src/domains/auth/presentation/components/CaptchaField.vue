<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { loadCaptchaScript } from '../../infrastructure/captchaApi'
import LocalCaptcha from './LocalCaptcha.vue'

const props = defineProps<{
  provider: string
  siteKey: string
}>()

const emit = defineEmits<{
  token: [value: string]
  fail: [message: string]
}>()

const host = ref<HTMLElement | null>(null)
const localRef = ref<{ reset: () => void; verify: () => Promise<string> } | null>(null)
let widgetId: string | number | null = null

type RenderFn = (el: HTMLElement, options: Record<string, unknown>) => string | number

interface CaptchaWindow extends Window {
  grecaptcha?: { ready: (cb: () => void) => void; render: RenderFn; reset: (id?: string | number) => void }
  hcaptcha?: { render: RenderFn; reset: (id?: string | number) => void }
  turnstile?: { render: RenderFn; reset: (id?: string | number) => void }
}

const scripts: Record<string, string> = {
  recaptcha: 'https://www.google.com/recaptcha/api.js?render=explicit',
  hcaptcha: 'https://js.hcaptcha.com/1/api.js?render=explicit',
  cloudflare: 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit',
}

function options(): Record<string, unknown> {
  return {
    sitekey: props.siteKey,
    callback: (token: string) => emit('token', token),
    'expired-callback': () => emit('token', ''),
    'error-callback': () => emit('fail', '验证码加载失败，请刷新后再试'),
  }
}

function renderWidget() {
  const w = window as CaptchaWindow
  const el = host.value
  if (!el) return
  if (props.provider === 'recaptcha' && w.grecaptcha) {
    w.grecaptcha.ready(() => {
      widgetId = w.grecaptcha!.render(el, options())
    })
    return
  }
  if (props.provider === 'hcaptcha' && w.hcaptcha) {
    widgetId = w.hcaptcha.render(el, options())
    return
  }
  if (props.provider === 'cloudflare' && w.turnstile) {
    widgetId = w.turnstile.render(el, options())
    return
  }
  emit('fail', '验证码加载失败，请刷新后再试')
}

async function mountWidget() {
  const src = scripts[props.provider]
  if (!src || !props.siteKey) {
    emit('fail', '验证码没有配好，暂时不能注册')
    return
  }
  try {
    await loadCaptchaScript(src)
    renderWidget()
  } catch (e) {
    emit('fail', e instanceof Error ? e.message : '验证码加载失败，请刷新后再试')
  }
}

function reset() {
  if (props.provider === 'local') {
    localRef.value?.reset()
    return
  }
  const w = window as CaptchaWindow
  emit('token', '')
  if (widgetId == null) return
  if (props.provider === 'recaptcha') w.grecaptcha?.reset(widgetId)
  else if (props.provider === 'hcaptcha') w.hcaptcha?.reset(widgetId)
  else w.turnstile?.reset(widgetId)
}

async function verify(): Promise<string> {
  if (props.provider !== 'local') return ''
  const token = await localRef.value?.verify()
  if (!token) throw new Error('验证码不正确，请再试一次')
  return token
}

defineExpose({ reset, verify })

onMounted(() => {
  if (props.provider !== 'local') void mountWidget()
})
</script>

<template>
  <LocalCaptcha
    v-if="provider === 'local'"
    ref="localRef"
    @token="emit('token', $event)"
    @fail="emit('fail', $event)"
  />
  <div v-else ref="host" class="captcha-field" />
</template>

<style scoped>
.captcha-field {
  display: flex;
  justify-content: center;
  min-height: 4.5rem;
  margin: var(--space-4) 0 var(--space-2);
}
</style>
