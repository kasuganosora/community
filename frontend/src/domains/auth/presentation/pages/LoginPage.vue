<script setup lang="ts">
import LanguagePicker from '@/shared/components/common/LanguagePicker.vue'
import { reactive, ref, computed, onMounted, nextTick } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '../stores/authStore'
import { isServerAuthReturn, safeInternalPath } from '@/shared/utils/authReturn'
import { passkeySupported } from '@/shared/utils/webauthn'
import { instanceApi } from '@/domains/instance/infrastructure/instanceApi'
import { captchaApi } from '../../infrastructure/captchaApi'
import CaptchaField from '../components/CaptchaField.vue'
import LogoIcon from '@/shared/components/common/LogoIcon.vue'

import { t } from '@/shared/i18n'
const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()

interface LoginForm {
  username: string
  password: string
}

const form = reactive<LoginForm>({
  username: typeof route.query.username === 'string' ? route.query.username : '',
  password: '',
})
const justRegistered = route.query.registered === '1'
// 添加账户: sign in another account while the current one stays signed in.
const addingAccount = route.query.add === '1' && authStore.isAuthenticated
const pendingApproval = route.query.pending === '1'
const emailVerify = route.query.verify === '1'
const registrationOpen = ref<boolean | null>(null)
const registrationMessage = ref('')
const captcha = reactive({ required: false, provider: '', siteKey: '' })
const captchaToken = ref('')
const captchaField = ref<{ reset: () => void; verify: () => Promise<string> } | null>(null)
const captchaNote = ref('')
const pending = ref(false)

onMounted(async () => {
  const [instanceResult, captchaResult] = await Promise.allSettled([
    instanceApi.getInstance(),
    captchaApi.getConfig(),
  ])
  if (instanceResult.status === 'fulfilled') {
    registrationOpen.value = instanceResult.value.registrations?.enabled !== false
    registrationMessage.value = instanceResult.value.registrations?.message?.trim() || ''
  } else {
    // Don't hide registration when the instance call itself fails; the
    // register page checks again and the server still refuses a closed site.
    registrationOpen.value = true
  }
  if (captchaResult.status === 'fulfilled') {
    const cfg = captchaResult.value
    if (cfg.enabled && cfg.provider !== 'none') {
      if (cfg.provider !== 'local' && !cfg.site_key) {
        captchaNote.value = t('auth.LoginPage.s18')
      } else {
        captcha.required = true
        captcha.provider = cfg.provider
        captcha.siteKey = cfg.site_key || 'local'
      }
    }
  }
})

const showPassword = ref(false)
const passwordIcon = computed(() => (showPassword.value ? 'browse-off' : 'browse'))
const canUsePasskey = passkeySupported()
const loginAttempts = ref(0)
const MAX_LOGIN_ATTEMPTS = 5
const LOCK_MS = 30_000

// Second step for accounts with two-factor authentication.
const twoFactorChallenge = ref('')
const twoFactorCode = ref('')
const codeInput = ref<HTMLInputElement | null>(null)

async function submitTwoFactor() {
  const code = twoFactorCode.value.trim()
  if (!code || pending.value || !twoFactorChallenge.value) return
  pending.value = true
  try {
    await authStore.loginTwoFactor(twoFactorChallenge.value, code)
    twoFactorChallenge.value = ''
    loginAttempts.value = 0
    continueAfterLogin()
  } catch (e) {
    twoFactorCode.value = ''
    pending.value = false
    // An expired challenge needs the password again.
    const api = (e as { apiMessage?: string })?.apiMessage
    if (api === 'invalid_challenge') cancelTwoFactor()
    else void nextTick(() => codeInput.value?.focus())
  }
}

function cancelTwoFactor() {
  twoFactorChallenge.value = ''
  twoFactorCode.value = ''
  form.password = ''
}

async function loginWithPasskey() {
  try {
    await authStore.loginWithPasskey()
    continueAfterLogin()
  } catch {
    // The store already surfaces the message.
  }
}

function goRegister() {
  const redirect = route.query.redirect
  router.push({
    name: 'register',
    query: typeof redirect === 'string' && redirect ? { redirect } : undefined,
  }).catch(() => {})
}

function continueAfterLogin() {
  if (addingAccount) {
    // Fresh page load so no store keeps the previous account's data.
    window.location.assign('/')
    return
  }
  const target = safeInternalPath(route.query.redirect)
  if (target && isServerAuthReturn(target)) {
    window.location.assign(target)
    return
  }
  router.push(target ?? { name: 'home' as const })
}

async function handleLogin() {
  if (loginAttempts.value >= MAX_LOGIN_ATTEMPTS) return
  if (captchaNote.value) return
  if (authStore.isAuthenticated && !addingAccount) {
    continueAfterLogin()
    return
  }
  if (pending.value) return
  if (captcha.required && captcha.provider !== 'local' && !captchaToken.value) return

  pending.value = true
  try {
    if (captcha.required && captcha.provider === 'local' && !captchaToken.value) {
      try {
        captchaToken.value = await captchaField.value?.verify() ?? ''
      } catch {
        pending.value = false
        return
      }
      if (!captchaToken.value) {
        pending.value = false
        return
      }
    }
    const result = await authStore.login({ ...form, captchaResponse: captchaToken.value || undefined })
    if (result && 'twoFactorRequired' in result) {
      twoFactorChallenge.value = result.challenge
      twoFactorCode.value = ''
      captchaToken.value = ''
      pending.value = false
      await nextTick()
      codeInput.value?.focus()
      return
    }
    loginAttempts.value = 0
    continueAfterLogin()
  } catch {
    if (authStore.isAuthenticated && !addingAccount) {
      continueAfterLogin()
      return
    }
    captchaToken.value = ''
    captchaField.value?.reset()
    loginAttempts.value++
    pending.value = false
    // The lock used to be permanent until a full page reload.
    if (loginAttempts.value >= MAX_LOGIN_ATTEMPTS) {
      setTimeout(() => { loginAttempts.value = 0 }, LOCK_MS)
    }
  }
}
</script>

<template>
  <div class="login-page">
    <div class="login-card">
      <div class="login-logo">
        <LogoIcon :size="40" />
      </div>

      <h1 class="login-title">{{ addingAccount ? $t('auth.LoginPage.s1') : $t('auth.LoginPage.s2') }}</h1>
      <p v-if="addingAccount" class="login-add-note">{{ $t('auth.LoginPage.s3') }}<button type="button" class="login-add-cancel" @click="router.back()">{{ $t('auth.LoginPage.s4') }}</button></p>

      <t-alert v-if="justRegistered && !authStore.error" theme="success" :message="$t('auth.LoginPage.s5')" style="margin-bottom: var(--space-4)" />
      <t-alert v-if="pendingApproval && !authStore.error" theme="info" :message="$t('auth.LoginPage.s6')" style="margin-bottom: var(--space-4)" />
      <t-alert v-if="emailVerify && !authStore.error" theme="info" :message="$t('auth.LoginPage.s7')" style="margin-bottom: var(--space-4)" />
      <t-alert v-if="authStore.error" theme="error" :message="authStore.error" style="margin-bottom: var(--space-4)" />

      <form v-if="twoFactorChallenge" class="login-form login-2fa" data-testid="login-2fa" @submit.prevent="submitTwoFactor">
        <p class="login-2fa__hint">{{ $t('auth.LoginPage.twoFactorHint') }}</p>
        <label for="login-2fa-code" class="sr-only">{{ $t('auth.LoginPage.twoFactorLabel') }}</label>
        <input
          id="login-2fa-code"
          ref="codeInput"
          v-model="twoFactorCode"
          class="login-2fa__input"
          name="otp"
          autocomplete="one-time-code"
          inputmode="text"
          autocapitalize="off"
          spellcheck="false"
          maxlength="32"
          :placeholder="$t('auth.LoginPage.twoFactorPlaceholder')"
        />
        <p class="login-2fa__backup">{{ $t('auth.LoginPage.twoFactorBackupHint') }}</p>
        <t-button theme="primary" type="submit" block size="large" shape="round" :loading="pending" :disabled="pending || !twoFactorCode.trim()">
          {{ $t('auth.LoginPage.twoFactorSubmit') }}
        </t-button>
        <button type="button" class="login-2fa__back" @click="cancelTwoFactor">{{ $t('auth.LoginPage.twoFactorBack') }}</button>
      </form>

      <t-form v-else @submit="handleLogin" class="login-form" label-width="0">
        <t-form-item>
          <t-input
            v-model="form.username"
            :placeholder="$t('auth.LoginPage.s8')"
            size="large"
            clearable
            autocomplete="username"
            :disabled="loginAttempts >= MAX_LOGIN_ATTEMPTS"
          />
        </t-form-item>
        <t-form-item>
          <t-input
            v-model="form.password"
            class="password-input"
            :type="showPassword ? 'text' : 'password'"
            :placeholder="$t('auth.LoginPage.s9')"
            size="large"
            autocomplete="current-password"
            :disabled="loginAttempts >= MAX_LOGIN_ATTEMPTS"
          >
            <!-- TDesign's built-in password eye is a bare SVG (mouse only), so it
                 is hidden and replaced by a real, labelled button. -->
            <template #suffix>
              <t-button
                variant="text"
                shape="square"
                size="small"
                class="password-toggle"
                :aria-label="showPassword ? $t('auth.LoginPage.s10') : $t('auth.LoginPage.s11')"
                :aria-pressed="showPassword"
                @click="showPassword = !showPassword"
              >
                <t-icon :name="passwordIcon" />
              </t-button>
            </template>
          </t-input>
        </t-form-item>
        <p v-if="loginAttempts >= MAX_LOGIN_ATTEMPTS" class="login-locked">
          {{ $t('auth.LoginPage.s12') }}
        </p>
        <p v-if="captchaNote" class="login-locked">{{ captchaNote }}</p>
        <CaptchaField
          v-if="captcha.required"
          ref="captchaField"
          :provider="captcha.provider"
          :site-key="captcha.siteKey"
          @token="captchaToken = $event"
          @fail="captchaNote = $event"
        />
        <t-form-item>
          <t-button
            theme="primary"
            type="submit"
            block
            size="large"
            shape="round"
            :loading="pending || authStore.loading"
            :disabled="pending || loginAttempts >= MAX_LOGIN_ATTEMPTS || !!captchaNote || (captcha.required && captcha.provider !== 'local' && !captchaToken)"
          >
            {{ $t('auth.LoginPage.s13') }}
          </t-button>
        </t-form-item>
      </t-form>

      <t-button
        v-if="canUsePasskey && !twoFactorChallenge"
        class="login-passkey"
        variant="outline"
        block
        size="large"
        shape="round"
        :loading="authStore.loading"
        @click="loginWithPasskey"
      >
        {{ $t('auth.LoginPage.s14') }}
      </t-button>

      <div v-if="!twoFactorChallenge && !justRegistered && !pendingApproval && registrationOpen === true" class="login-account">
        <p class="login-account__q">{{ $t('auth.LoginPage.s15') }}</p>
        <t-button
          variant="outline"
          block
          size="large"
          shape="round"
          @click="goRegister"
        >
          {{ $t('auth.LoginPage.s16') }}
        </t-button>
      </div>
      <p v-else-if="!twoFactorChallenge && !justRegistered && registrationOpen === false" class="login-account__q login-closed">
        {{ registrationMessage || $t('auth.LoginPage.s17') }}
      </p>
      <div class="auth-lang"><LanguagePicker compact /></div>
    </div>
  </div>
</template>

<style scoped>
.login-2fa { display: flex; flex-direction: column; gap: var(--space-3); }
.login-2fa__hint { margin: 0; text-align: center; color: var(--color-label); }
.login-2fa__input {
  width: 100%;
  min-height: 48px;
  padding: 0 var(--space-4);
  border: 1px solid var(--color-separator-opaque, var(--color-separator));
  border-radius: var(--radius-md, 10px);
  background: var(--color-bg-elevated, var(--color-bg-row));
  color: var(--color-label);
  font-size: 1.375rem;
  letter-spacing: 0.2em;
  text-align: center;
  font-variant-numeric: tabular-nums;
}
.login-2fa__input:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 1px; }
.login-2fa__backup { margin: 0; font-size: var(--text-footnote); color: var(--color-label-secondary); text-align: center; }
.login-2fa__back { align-self: center; min-height: var(--hit-target, 44px); background: none; border: 0; color: var(--color-accent); cursor: pointer; }
.auth-lang { display: flex; justify-content: center; margin-top: var(--space-4, 16px); }
.login-add-note {
  margin: calc(-1 * var(--space-2)) 0 var(--space-4);
  text-align: center;
  font-size: var(--text-footnote);
  color: var(--color-label-secondary);
}
.login-add-cancel {
  margin-left: var(--space-2);
  color: var(--color-accent);
}

.password-input :deep(.t-input__suffix-icon) {
  display: none;
}

.login-page {
  min-height: 100dvh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: calc(var(--space-6) + var(--safe-top)) var(--gutter) calc(var(--space-6) + var(--safe-bottom));
  background: var(--color-bg-grouped);
}

/* One elevated surface, generous padding, a single primary action. */
.login-card {
  width: 100%;
  max-width: 25rem;
  padding: var(--space-8) var(--space-6) var(--space-6);
  border-radius: var(--radius-xl);
  background: var(--color-bg-row);
  box-shadow: var(--shadow-2);
}

.login-logo {
  display: flex;
  justify-content: center;
  margin-bottom: var(--space-4);
  color: var(--color-label);
}

.login-title {
  font-size: var(--text-title-2);
  line-height: var(--leading-title-2);
  letter-spacing: var(--tracking-title-2);
  font-weight: var(--weight-bold);
  text-align: center;
  margin-bottom: var(--space-6);
  color: var(--color-label);
}

.login-form :deep(.t-form__item) {
  margin-bottom: var(--space-3);
}

.login-form :deep(.t-form__item:last-child) {
  margin-top: var(--space-5);
  margin-bottom: 0;
}

.login-form :deep(.t-form__controls) {
  margin-left: 0 !important;
}

.login-form :deep(.t-button--theme-primary) {
  font-weight: var(--weight-semibold);
}

.login-passkey {
  margin-top: var(--space-3);
}

.login-account {
  margin-top: var(--space-8);
  text-align: center;
}

.login-account__q {
  margin-bottom: var(--space-3);
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
}

.login-closed {
  margin-top: var(--space-8);
  text-align: center;
}

.login-locked {
  color: var(--color-danger);
  font-size: var(--text-footnote);
  text-align: center;
  margin-bottom: var(--space-3);
}

@media (max-width: 30rem) {
  /* Phones: the page is the surface; no floating card. */
  .login-page {
    align-items: flex-start;
    background: var(--color-bg);
  }

  .login-card {
    padding: var(--space-8) 0 0;
    background: transparent;
    box-shadow: none;
  }
}
</style>
