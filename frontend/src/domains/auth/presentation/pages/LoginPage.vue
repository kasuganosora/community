<script setup lang="ts">
import { reactive, ref, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '../stores/authStore'
import { isServerAuthReturn, safeInternalPath } from '@/shared/utils/authReturn'
import { passkeySupported } from '@/shared/utils/webauthn'
import { instanceApi } from '@/domains/instance/infrastructure/instanceApi'
import { captchaApi } from '../../infrastructure/captchaApi'
import CaptchaField from '../components/CaptchaField.vue'
import LogoIcon from '@/shared/components/common/LogoIcon.vue'

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
const pendingApproval = route.query.pending === '1'
const emailVerify = route.query.verify === '1'
const registrationOpen = ref<boolean | null>(null)
const registrationMessage = ref('')
const captcha = reactive({ required: false, provider: '', siteKey: '' })
const captchaToken = ref('')
const captchaField = ref<{ reset: () => void; verify: () => Promise<string> } | null>(null)
const captchaNote = ref('')

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
        captchaNote.value = '验证码没有配好，暂时不能登录。'
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
  if (captcha.required && captcha.provider !== 'local' && !captchaToken.value) return

  try {
    if (captcha.required && captcha.provider === 'local' && !captchaToken.value) {
      try {
        captchaToken.value = await captchaField.value?.verify() ?? ''
      } catch {
        return
      }
      if (!captchaToken.value) return
    }
    await authStore.login({ ...form, captchaResponse: captchaToken.value || undefined })
    loginAttempts.value = 0
    continueAfterLogin()
  } catch {
    captchaToken.value = ''
    captchaField.value?.reset()
    loginAttempts.value++
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

      <h1 class="login-title">登录社区</h1>

      <t-alert v-if="justRegistered && !authStore.error" theme="success" message="注册成功，请登录。" style="margin-bottom: var(--space-4)" />
      <t-alert v-if="pendingApproval && !authStore.error" theme="info" message="注册已提交，管理员通过后才能登录。" style="margin-bottom: var(--space-4)" />
      <t-alert v-if="emailVerify && !authStore.error" theme="info" message="验证邮件已发出，请先点邮件里的链接，再登录。" style="margin-bottom: var(--space-4)" />
      <t-alert v-if="authStore.error" theme="error" :message="authStore.error" style="margin-bottom: var(--space-4)" />

      <t-form @submit="handleLogin" class="login-form" label-width="0">
        <t-form-item>
          <t-input
            v-model="form.username"
            placeholder="用户名"
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
            placeholder="密码"
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
                :aria-label="showPassword ? '隐藏密码' : '显示密码'"
                :aria-pressed="showPassword"
                @click="showPassword = !showPassword"
              >
                <t-icon :name="passwordIcon" />
              </t-button>
            </template>
          </t-input>
        </t-form-item>
        <p v-if="loginAttempts >= MAX_LOGIN_ATTEMPTS" class="login-locked">
          尝试次数太多了，请在 30 秒后再试。
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
            :loading="authStore.loading"
            :disabled="loginAttempts >= MAX_LOGIN_ATTEMPTS || !!captchaNote || (captcha.required && captcha.provider !== 'local' && !captchaToken)"
          >
            登录
          </t-button>
        </t-form-item>
      </t-form>

      <t-button
        v-if="canUsePasskey"
        class="login-passkey"
        variant="outline"
        block
        size="large"
        shape="round"
        :loading="authStore.loading"
        @click="loginWithPasskey"
      >
        使用通行密钥登录
      </t-button>

      <div v-if="!justRegistered && !pendingApproval && registrationOpen === true" class="login-account">
        <p class="login-account__q">有本站账号就在上面登录。还没有？</p>
        <t-button
          variant="outline"
          block
          size="large"
          shape="round"
          @click="goRegister"
        >
          没有，去注册
        </t-button>
      </div>
      <p v-else-if="!justRegistered && registrationOpen === false" class="login-account__q login-closed">
        {{ registrationMessage || '本站暂不开放注册。' }}
      </p>
    </div>
  </div>
</template>

<style scoped>
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
