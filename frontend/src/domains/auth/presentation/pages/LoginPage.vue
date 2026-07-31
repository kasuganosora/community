<script setup lang="ts">
import { reactive, ref, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '../stores/authStore'
import LogoIcon from '@/shared/components/common/LogoIcon.vue'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()

interface LoginForm {
  username: string
  password: string
}

const form = reactive<LoginForm>({
  username: '',
  password: '',
})

const showPassword = ref(false)
const passwordIcon = computed(() => (showPassword.value ? 'browse-off' : 'browse'))
const loginAttempts = ref(0)
const MAX_LOGIN_ATTEMPTS = 5

async function handleLogin() {
  if (loginAttempts.value >= MAX_LOGIN_ATTEMPTS) {
    return
  }

  try {
    await authStore.login(form)
    // Reset attempts on successful login
    loginAttempts.value = 0
    // Redirect to the originally requested page if available
    const redirectPath = route.query.redirect as string | undefined
    const safePath = redirectPath && redirectPath.startsWith('/') && !redirectPath.startsWith('//')
      ? redirectPath
      : { name: 'home' as const }
    router.push(safePath)
  } catch {
    loginAttempts.value++
  }
}
</script>

<template>
  <div class="login-page">
    <div class="login-card">
      <div class="login-logo">
        <LogoIcon :size="40" />
      </div>

      <h1 class="login-title">Sign in to Community</h1>

      <t-alert v-if="authStore.error" theme="error" :message="authStore.error" style="margin-bottom: var(--space-4)" />

      <t-form @submit="handleLogin" class="login-form" label-width="0">
        <t-form-item>
          <t-input
            v-model="form.username"
            placeholder="Username"
            size="large"
            clearable
            autocomplete="username"
            :disabled="loginAttempts >= MAX_LOGIN_ATTEMPTS"
          />
        </t-form-item>
        <t-form-item>
          <t-input
            v-model="form.password"
            :type="showPassword ? 'text' : 'password'"
            placeholder="Password"
            size="large"
            autocomplete="current-password"
            :disabled="loginAttempts >= MAX_LOGIN_ATTEMPTS"
          >
            <template #suffixIcon>
              <t-button 
                variant="text" 
                shape="square" 
                size="small"
                @click="showPassword = !showPassword"
                :icon="passwordIcon"
              />
            </template>
          </t-input>
        </t-form-item>
        <p v-if="loginAttempts >= MAX_LOGIN_ATTEMPTS" class="login-locked">
          Too many failed attempts. Please try again later.
        </p>
        <t-form-item>
          <t-button
            theme="primary"
            type="submit"
            block
            size="large"
            shape="round"
            :loading="authStore.loading"
            :disabled="loginAttempts >= MAX_LOGIN_ATTEMPTS"
          >
            Sign in
          </t-button>
        </t-form-item>
      </t-form>

      <div class="login-footer">
        Don't have an account?
        <router-link to="/register" class="login-link">Sign up</router-link>
      </div>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-primary);
  padding: var(--space-5);
}

.login-card {
  width: 100%;
  max-width: 400px;
  padding: var(--space-8);
}

.login-logo {
  text-align: center;
  margin-bottom: var(--space-6);
  color: var(--text-primary);
}

.login-title {
  font-size: var(--font-2xl);
  font-weight: 700;
  text-align: center;
  margin-bottom: var(--space-8);
  color: var(--text-primary);
}

.login-form :deep(.t-form__item) {
  margin-bottom: var(--space-4);
}

.login-form :deep(.t-form__controls) {
  margin-left: 0 !important;
}

.login-form :deep(.t-input) {
  background: var(--bg-secondary) !important;
  border-radius: var(--radius-md) !important;
  padding: 0 var(--space-3) !important;
}

.login-form :deep(.t-button--theme-primary) {
  background: linear-gradient(135deg, #86e01e, #67b90c) !important;
  border: none !important;
  color: #000 !important;
  font-weight: 700 !important;
}

.login-footer {
  text-align: center;
  margin-top: var(--space-6);
  color: var(--text-secondary);
  font-size: var(--font-sm);
}

.login-link {
  color: var(--accent-blue);
  margin-left: var(--space-1);
}

.login-locked {
  color: var(--danger);
  font-size: var(--font-sm);
  text-align: center;
  margin-bottom: var(--space-4);
}
</style>
