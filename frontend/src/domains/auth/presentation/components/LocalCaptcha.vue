<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { captchaApi, solveCaptchaPow } from '../../infrastructure/captchaApi'

const emit = defineEmits<{
  token: [value: string]
  fail: [message: string]
}>()

const imageUrl = ref('')
const index = ref(0)
const total = ref(5)
const finished = ref(false)
const passed = ref(false)
const busy = ref(false)
const answer = ref('')
const hint = ref('正在准备验证码')
const prompt = ref('一共 5 个字母或数字。只认那块深浅相间、在波动的字。浅色花纹和小字不算。')
const homepage = ref('')
let generation = 0
let timer = 0
let challengeId = ''
let powTask: Promise<string> | null = null
let lastToken = ''

function clearTimer() {
  if (timer) {
    window.clearTimeout(timer)
    timer = 0
  }
}

function sleep(ms: number, gen: number) {
  clearTimer()
  return new Promise<void>((resolve) => {
    timer = window.setTimeout(() => {
      timer = 0
      resolve()
    }, Math.max(0, ms))
    void gen
  })
}

function replaceImage(next: string) {
  if (imageUrl.value) URL.revokeObjectURL(imageUrl.value)
  imageUrl.value = next
}

async function play(gen: number, id: string) {
  while (gen === generation) {
    const frame = await captchaApi.fetchLocalFrame(id)
    if (gen !== generation) {
      if (frame.kind === 'image') URL.revokeObjectURL(frame.url)
      return
    }
    if (frame.kind === 'wait') {
      if (frame.done) {
        finished.value = true
        hint.value = '五个都看完了，按出现顺序填进去'
        return
      }
      hint.value = index.value === 0 ? '正在准备验证码' : '下一个字符马上出现'
      await sleep(frame.waitMs, gen)
      continue
    }
    replaceImage(frame.url)
    index.value = frame.index
    total.value = frame.total
    hint.value = frame.done
      ? '五个都看完了，按出现顺序填进去'
      : `第 ${frame.index}/${frame.total} 个`
    if (frame.done) {
      finished.value = true
      return
    }
    await sleep(frame.waitMs, gen)
  }
}

async function start() {
  const gen = ++generation
  clearTimer()
  finished.value = false
  passed.value = false
  answer.value = ''
  index.value = 0
  hint.value = '正在准备验证码'
  emit('token', '')
  lastToken = ''
  replaceImage('')
  try {
    const started = await captchaApi.startLocal()
    if (gen !== generation) return
    challengeId = started.id
    total.value = started.total
    if (started.prompt) prompt.value = started.prompt
    powTask = solveCaptchaPow(started.pow_salt, started.pow_bits || 14)
    await play(gen, started.id)
  } catch (e) {
    if (gen !== generation) return
    const msg = e instanceof Error ? e.message : '验证码加载失败，请换一个'
    hint.value = msg
    emit('fail', msg)
  }
}

async function verify(): Promise<string> {
  if (passed.value && lastToken) return lastToken
  if (!finished.value || !challengeId) {
    hint.value = '请先看完五个字'
    throw new Error('请先看完五个字')
  }
  if (answer.value.trim().length < 5) {
    hint.value = '请填入 5 个字母或数字'
    throw new Error('请填入 5 个字母或数字')
  }
  const gen = generation
  const id = challengeId
  const task = powTask
  busy.value = true
  try {
    const nonce = task ? await task : ''
    if (gen !== generation) throw new Error('验证码已更换')
    const { token } = await captchaApi.verifyLocal(id, answer.value, nonce, homepage.value)
    if (gen !== generation) throw new Error('验证码已更换')
    passed.value = true
    lastToken = token
    hint.value = '验证码已通过'
    emit('token', token)
    return token
  } catch (e) {
    if (gen !== generation) throw e
    emit('token', '')
    const msg = e instanceof Error ? e.message : '验证码不正确，请再试一次'
    hint.value = msg
    throw new Error(msg)
  } finally {
    busy.value = false
  }
}

function reset() {
  void start()
}

defineExpose({ reset, verify })
onMounted(start)
onBeforeUnmount(() => {
  generation++
  clearTimer()
  replaceImage('')
})
</script>

<template>
  <div class="local-captcha">
    <p class="local-captcha__lead">{{ prompt }}</p>
    <div class="local-captcha__stage" aria-hidden="true">
      <img v-if="imageUrl" :src="imageUrl" alt="" class="local-captcha__gif" />
    </div>
    <p class="local-captcha__hint">{{ hint }}</p>
    <label class="local-captcha__label">
      <span class="sr-only">验证码</span>
      <input
        v-model="answer"
        class="local-captcha__input"
        maxlength="8"
        autocomplete="off"
        autocapitalize="characters"
        spellcheck="false"
        placeholder="5 个字母或数字"
        :disabled="passed"
      />
    </label>
    <input v-model="homepage" class="local-captcha__trap" tabindex="-1" autocomplete="off" aria-hidden="true" />
    <div class="local-captcha__actions">
      <button type="button" class="local-captcha__again" :disabled="busy" @click="reset">
        看不清，换一个
      </button>
    </div>
  </div>
</template>

<style scoped>
.local-captcha {
  margin: var(--space-4) 0 var(--space-2);
}

.local-captcha__lead,
.local-captcha__hint {
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  text-align: center;
}

.local-captcha__stage {
  margin: var(--space-3) 0;
  border-radius: var(--radius-lg);
  background: #f4f4f6;
  overflow: hidden;
}

.local-captcha__gif {
  display: block;
  width: 100%;
  height: auto;
  aspect-ratio: 240 / 128;
}

.local-captcha__label {
  display: block;
}

.local-captcha__input {
  width: 100%;
  min-height: var(--hit-target);
  margin-top: var(--space-3);
  padding: 0 var(--space-4);
  border: 0;
  border-radius: var(--radius-full);
  background: var(--color-bg-grouped);
  color: var(--color-label);
  font-size: 1rem;
  letter-spacing: 0.18em;
  text-align: center;
  text-transform: uppercase;
}

.local-captcha__actions {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-3);
}

.local-captcha__again {
  width: 100%;
  min-height: var(--hit-target);
  padding: 0 var(--space-4);
  border: 0;
  border-radius: var(--radius-full);
  background: var(--color-bg-grouped);
  color: var(--color-label);
  font-weight: var(--weight-semibold);
  touch-action: manipulation;
}

.local-captcha__trap {
  position: absolute;
  left: -9999px;
  height: 0;
  width: 0;
  opacity: 0;
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
</style>
