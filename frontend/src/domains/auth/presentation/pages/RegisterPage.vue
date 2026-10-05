<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { authApi } from '../../infrastructure/authApi'
import { captchaApi } from '../../infrastructure/captchaApi'
import { instanceApi, DEFAULT_RULES, type InstanceRule } from '@/domains/instance/infrastructure/instanceApi'
import CaptchaField from '../components/CaptchaField.vue'

const router = useRouter()
const route = useRoute()

const step = ref<'rules' | 'form' | 'closed'>('rules')

// Server rules come from /api/v1/instance/rules (they were hard-coded);
// the defaults are only shown when the admin has not configured any.
const rules = ref<InstanceRule[]>(DEFAULT_RULES)
const serverDomain = ref(window.location.host)
const registrationMessage = ref('')
const inviteRequired = ref(false)
const approvalRequired = ref(false)
const captcha = reactive({ required: false, provider: '', siteKey: '' })
const captchaToken = ref('')
const captchaField = ref<{ reset: () => void; verify: () => Promise<string> } | null>(null)
const error = ref<string | null>(null)

onMounted(async () => {
  const [rulesResult, instanceResult, captchaResult] = await Promise.allSettled([
    instanceApi.getRules(),
    instanceApi.getInstance(),
    captchaApi.getConfig(),
  ])
  if (rulesResult.status === 'fulfilled' && rulesResult.value.length) {
    rules.value = rulesResult.value
  }
  if (instanceResult.status === 'fulfilled') {
    const info = instanceResult.value
    const domain = info.domain || info.uri
    if (domain) {
      try {
        serverDomain.value = new URL(domain.includes('://') ? domain : `https://${domain}`).host
      } catch {
        // Keep the current host when instance metadata is malformed.
      }
    }
    const registrations = info.registrations
    if (registrations?.enabled === false) {
      registrationMessage.value = registrations.message?.trim() || ''
      step.value = 'closed'
    }
    inviteRequired.value = registrations?.invite_required === true
    approvalRequired.value = registrations?.approval_required === true
  }
  if (captchaResult.status === 'fulfilled') {
    const cfg = captchaResult.value
    if (cfg.enabled && cfg.provider !== 'none') {
      if (cfg.provider !== 'local' && !cfg.site_key) {
        error.value = '验证码没有配好，暂时不能注册。'
      } else {
        captcha.required = true
        captcha.provider = cfg.provider
        captcha.siteKey = cfg.site_key || 'local'
      }
    }
  }
})

const rulesAgreed = ref(false)
const tosAgreed = ref(false)
const canContinue = computed(() => rulesAgreed.value && tosAgreed.value)

// Registration form
const form = reactive({
  username: '',
  email: '',
  password: '',
  passwordConfirm: '',
  inviteCode: '',
})

const registering = ref(false)

// Email validation regex
const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
const isValidEmail = computed(() => emailRegex.test(form.email))

// Password validation: at least 8 chars, one uppercase, one lowercase, one number
const passwordRegex = /^(?=.*[a-z])(?=.*[A-Z])(?=.*\d).{8,}$/
const isStrongPassword = computed(() => passwordRegex.test(form.password))

const passwordMatch = computed(() => form.password === form.passwordConfirm)
const isValidUsername = computed(() => /^[\p{L}\p{N}_]{2,20}$/u.test(form.username))

// Fixed: Check for non-empty password confirm correctly
const showPasswordMismatch = computed(() => 
  form.passwordConfirm !== '' && !passwordMatch.value
)

const canRegister = computed(() =>
  isValidUsername.value &&
  isValidEmail.value &&
  isStrongPassword.value &&
  passwordMatch.value &&
  (!inviteRequired.value || form.inviteCode.trim() !== '') &&
  (!captcha.required || captcha.provider === 'local' || captchaToken.value !== '') &&
  !error.value?.includes('验证码没有配好') &&
  !registering.value,
)

function explainRegisterError(e: unknown): string {
  const msg = e instanceof Error ? e.message : ''
  if (/暂不开放|not open/i.test(msg)) return '本站暂不开放注册。'
  if (/验证码|captcha/i.test(msg)) return '验证码没有通过，请再试一次。'
  if (/invite/i.test(msg)) return '需要有效的邀请码。'
  return msg || '注册失败，请检查填写内容后重试'
}

function proceedToForm() {
  if (canContinue.value) {
    step.value = 'form'
  }
}

async function handleRegister() {
  if (!canRegister.value) return
  registering.value = true
  error.value = null
  try {
    if (captcha.required && captcha.provider === 'local' && !captchaToken.value) {
      try {
        captchaToken.value = await captchaField.value?.verify() ?? ''
      } catch {
        return
      }
      if (!captchaToken.value) return
    }
    const result = await authApi.register({
      username: form.username,
      email: form.email,
      password: form.password,
      passwordConfirm: form.passwordConfirm,
      inviteCode: form.inviteCode.trim() || undefined,
      captchaResponse: captchaToken.value || undefined,
    })
    const redirect = typeof route.query.redirect === 'string' ? { redirect: route.query.redirect } : {}
    const query = { username: form.username, ...redirect }
    if (result.approval_required) {
      await router.push({ name: 'login', query: { ...query, pending: '1' } })
    } else if (result.message?.includes('verify email')) {
      await router.push({ name: 'login', query: { ...query, verify: '1' } })
    } else {
      await router.push({ name: 'login', query: { ...query, registered: '1' } })
    }
  } catch (e: unknown) {
    captchaToken.value = ''
    captchaField.value?.reset()
    error.value = explainRegisterError(e)
  } finally {
    registering.value = false
  }
}

function goBack() {
  if (step.value === 'form') {
    step.value = 'rules'
  } else {
    router.push({ name: 'landing' })
  }
}

function close() {
  router.push({ name: 'landing' })
}
</script>

<template>
  <div class="register-page">
    <div class="register-modal">
      <!-- Header -->
      <div class="register-header">
        <button type="button" aria-label="关闭注册" class="register-header__close" @click="close">
          <t-icon name="close" :size="20" />
        </button>
        <h1 class="register-header__title">注册</h1>
      </div>

      <div v-if="step === 'closed'" class="register-body">
        <p class="rules-intro">
          <strong>本站暂不开放注册。</strong>
        </p>
        <p v-if="registrationMessage" class="rules-note">{{ registrationMessage }}</p>
        <div class="register-actions register-actions--center">
          <t-button theme="primary" shape="round" size="large" @click="router.push({ name: 'login' })">
            返回登录
          </t-button>
        </div>
      </div>

      <!-- Step 1: Rules -->
      <div v-if="step === 'rules'" class="register-body">
        <div class="rules-icon">
          <svg viewBox="0 0 24 24" width="48" height="48" fill="var(--accent-green)">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6zm-1 2l5 5h-5V4zM6 20V4h5v7h7v9H6z"/>
          </svg>
        </div>

        <p class="rules-intro">
          注册前请先阅读并同意下面的内容。
          <strong>请仔细看完，这些规则会生效。</strong>
        </p>

        <!-- Server rules -->
        <div class="rules-section">
          <div class="rules-section__header">
            <span>服务器规则</span>
            <t-icon name="chevron-up" :size="16" />
          </div>
          <div class="rules-list">
            <div v-for="(rule, i) in rules" :key="rule.id" class="rule-item">
              <span class="rule-number">{{ i + 1 }}</span>
              <span class="rule-text">{{ rule.text }}</span>
            </div>
          </div>
          <label class="agree-check">
            <input type="checkbox" v-model="rulesAgreed" class="agree-check__input" />
            <span class="agree-check__label">我同意</span>
          </label>
        </div>

        <!-- ToS -->
        <div class="rules-section">
          <div class="rules-section__header">
            <span>服务条款</span>
            <t-icon name="chevron-up" :size="16" />
          </div>
          <div class="rules-tos">
            <!-- New tab, so the registration progress isn't lost. -->
            <a href="/about#terms" target="_blank" rel="noopener" class="tos-link">
              查看服务条款
              <t-icon name="link" :size="14" />
            </a>
          </div>
          <label class="agree-check">
            <input type="checkbox" v-model="tosAgreed" class="agree-check__input" />
            <span class="agree-check__label">我同意</span>
          </label>
        </div>

        <p class="rules-note">需要同意以上全部内容才能继续。</p>

        <div class="register-actions">
          <t-button
            theme="default"
            shape="round"
            size="large"
            @click="goBack"
          >
            取消
          </t-button>
          <t-button
            theme="primary"
            shape="round"
            size="large"
            :disabled="!canContinue"
            @click="proceedToForm"
            class="btn-continue"
          >
            继续
          </t-button>
        </div>
      </div>

      <!-- Step 2: Registration form -->
      <div v-if="step === 'form'" class="register-body">
        <div class="form-icon">
          <svg viewBox="0 0 24 24" width="48" height="48" fill="var(--accent-green)">
            <path d="M15 14c-2.67 0-8 1.33-8 4v2h16v-2c0-2.67-5.33-4-8-4zm0-2a4 4 0 1 0 0-8 4 4 0 0 0 0 8z"/>
          </svg>
        </div>

        <t-alert v-if="error" theme="error" :message="error" style="margin-bottom: var(--space-4)" />

        <t-form @submit="handleRegister" class="register-form" label-align="top">
          <t-form-item label="用户名" help="2–20 个字母、数字或下划线。注册后不能修改。">
            <t-input
              v-model="form.username"
              size="large"
              placeholder="username"
              autocomplete="username"
            >
              <template #prefixIcon>
                <span class="input-prefix">@</span>
              </template>
              <template #suffix>
                <span class="input-suffix" :title="`@${serverDomain}`">@{{ serverDomain }}</span>
              </template>
            </t-input>
          </t-form-item>

          <t-form-item label="邮箱">
            <t-input
              v-model="form.email"
              type="text"
              size="large"
              placeholder="email@example.com"
              autocomplete="email"
            >
              <template #prefixIcon>
                <t-icon name="mail" />
              </template>
            </t-input>
          </t-form-item>

          <t-form-item label="密码" help="至少 8 位，并包含大写字母、小写字母和数字。">
            <t-input
              v-model="form.password"
              type="password"
              size="large"
              placeholder="至少 8 个字符"
              autocomplete="new-password"
            >
              <template #prefixIcon>
                <t-icon name="lock-on" />
              </template>
            </t-input>
          </t-form-item>

          <t-form-item label="确认密码">
            <t-input
              v-model="form.passwordConfirm"
              type="password"
              size="large"
              placeholder="再输入一次密码"
              autocomplete="new-password"
              :status="showPasswordMismatch ? 'error' : 'default'"
            >
              <template #prefixIcon>
                <t-icon name="lock-on" />
              </template>
            </t-input>
            <div v-if="showPasswordMismatch" class="form-error">
              两次输入的密码不一致
            </div>
          </t-form-item>

          <t-form-item v-if="inviteRequired" label="邀请码">
            <t-input
              v-model="form.inviteCode"
              size="large"
              placeholder="向邀请你的人索取"
              autocomplete="off"
            />
          </t-form-item>

          <p v-if="approvalRequired" class="rules-note">提交后需要管理员通过，通过之前不能登录。</p>

          <CaptchaField
            v-if="captcha.required"
            ref="captchaField"
            :provider="captcha.provider"
            :site-key="captcha.siteKey"
            @token="captchaToken = $event"
            @fail="error = $event"
          />

          <div class="register-actions register-actions--center">
            <t-button
              theme="primary"
              shape="round"
              size="large"
              type="submit"
              :disabled="!canRegister"
              :loading="registering"
              class="btn-continue"
            >
              开始使用
            </t-button>
          </div>
        </t-form>
      </div>
    </div>
  </div>
</template>

<style scoped>
.register-page {
  min-height: 100dvh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: calc(var(--space-6) + var(--safe-top)) var(--gutter) calc(var(--space-6) + var(--safe-bottom));
  background: var(--color-bg-grouped);
}

/* Presented like a form sheet: elevated surface, translucent sticky bar. */
.register-modal {
  width: 100%;
  max-width: 32.5rem;
  max-height: calc(100dvh - var(--space-12));
  overflow-y: auto;
  overscroll-behavior: contain;
  border-radius: var(--radius-xl);
  background: var(--color-bg-row);
  box-shadow: var(--shadow-2);
}

.register-header {
  position: sticky;
  top: 0;
  z-index: 1;
  display: grid;
  grid-template-columns: var(--hit-target) 1fr var(--hit-target);
  align-items: center;
  min-height: var(--bar-height);
  padding: 0 var(--space-2);
  background: var(--material-bar);
  -webkit-backdrop-filter: var(--material-blur-bar);
  backdrop-filter: var(--material-blur-bar);
  box-shadow: 0 var(--hairline) 0 var(--color-separator);
}

.register-header__close {
  display: grid;
  place-items: center;
  width: var(--hit-target);
  height: var(--hit-target);
  border-radius: var(--radius-full);
  color: var(--color-label-secondary);
  transition:
    background-color var(--duration-fast) var(--ease-out),
    transform var(--duration-release) var(--ease-out);
}

@media (hover: hover) and (pointer: fine) {
  .register-header__close:hover {
    background: var(--color-hover);
  }
}

.register-header__close:active {
  background: var(--color-pressed);
  transform: scale(var(--press-scale-small));
  transition-duration: var(--duration-press);
}

.register-header__title {
  text-align: center;
  font-size: var(--text-headline);
  letter-spacing: var(--tracking-headline);
  font-weight: var(--weight-semibold);
  color: var(--color-label);
}

.register-body {
  padding: var(--space-6) var(--space-6) var(--space-8);
}

.rules-icon,
.form-icon {
  display: flex;
  justify-content: center;
  margin-bottom: var(--space-4);
}

/* Replace the old lime glyph fill with the single app accent. */
.rules-icon svg,
.form-icon svg {
  fill: var(--color-accent);
  width: 2.75rem;
  height: 2.75rem;
}

.rules-intro {
  text-align: center;
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
  line-height: var(--leading-subhead);
  margin-bottom: var(--space-6);
  text-wrap: pretty;
}

.rules-intro strong {
  display: block;
  margin-top: var(--space-1);
  color: var(--color-label);
  font-weight: var(--weight-semibold);
}

/* Inset grouped sections, iOS Settings style. */
.rules-section {
  margin-bottom: var(--space-4);
  border-radius: var(--radius-md);
  background: var(--color-bg-grouped);
  overflow: hidden;
}

.rules-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-3) var(--space-4) var(--space-1);
  font-size: var(--text-footnote);
  letter-spacing: var(--tracking-footnote);
  font-weight: var(--weight-semibold);
  text-transform: uppercase;
  color: var(--color-label-secondary);
}

.rules-section__header :deep(svg) {
  display: none;
}

.rules-list {
  padding: 0 var(--space-4) var(--space-2);
}

.rule-item {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-2) 0;
}

.rule-number {
  display: grid;
  place-items: center;
  width: 1.5rem;
  height: 1.5rem;
  margin-top: 1px;
  border-radius: 50%;
  background: var(--color-fill-strong);
  color: var(--color-label);
  font-size: var(--text-caption);
  font-weight: var(--weight-semibold);
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}

.rule-text {
  font-size: var(--text-body);
  line-height: var(--leading-body);
  color: var(--color-label);
}

.agree-check {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-height: var(--hit-target);
  padding: 0 var(--space-4);
  box-shadow: inset 0 var(--hairline) 0 var(--color-separator);
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: background-color var(--duration-release) var(--ease-out);
}

.agree-check:active {
  background: var(--color-pressed);
  transition-duration: 0s;
}

.agree-check__input {
  width: 1.25rem;
  height: 1.25rem;
  accent-color: var(--color-accent);
  cursor: pointer;
}

.agree-check__label {
  font-size: var(--text-body);
  color: var(--color-label);
}

.rules-tos {
  padding: var(--space-1) var(--space-4) var(--space-3);
}

.tos-link {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--color-accent-text);
  font-size: var(--text-body);
}

.rules-note {
  text-align: center;
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  margin-bottom: var(--space-5);
}

.register-actions {
  display: flex;
  justify-content: center;
  gap: var(--space-3);
}

.register-actions :deep(.t-button) {
  flex: 1;
  max-width: 11rem;
  margin: 0;
  font-weight: var(--weight-semibold);
}

.register-actions--center {
  justify-content: center;
  margin-top: var(--space-2);
}

.btn-continue {
  min-width: 7.5rem;
}

.register-form :deep(.t-form__label) {
  padding: 0 !important;
  width: auto !important;
  margin-bottom: var(--space-1);
  color: var(--color-label-secondary) !important;
  font-size: var(--text-footnote);
  font-weight: var(--weight-medium);
  letter-spacing: var(--tracking-footnote);
}

.register-form :deep(.t-form__item) {
  margin-bottom: var(--space-4);
}

.register-form :deep(.t-form__controls) {
  margin-left: 0 !important;
}

.register-form :deep(.t-form__controls-content) {
  flex-wrap: wrap;
}

.register-form :deep(.t-input__tips) {
  color: var(--color-label-secondary) !important;
}

.input-prefix {
  color: var(--color-label-secondary);
}

.input-suffix {
  display: block;
  max-width: min(36vw, 12rem);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  font-variant-numeric: tabular-nums;
}

.form-error {
  flex-basis: 100%;
  margin-top: var(--space-1);
  font-size: var(--text-footnote);
  color: var(--color-danger);
}

@media (max-width: 30rem) {
  /* Phones: the form is the page. */
  .register-page {
    align-items: stretch;
    padding: 0;
    background: var(--color-bg-row);
  }

  .register-modal {
    max-width: none;
    max-height: none;
    border-radius: 0;
    box-shadow: none;
  }

  .register-header {
    padding-top: var(--safe-top);
  }

  .register-body {
    padding: var(--space-5) var(--gutter) calc(var(--space-8) + var(--safe-bottom));
  }
}
</style>
