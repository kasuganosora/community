<script setup lang="ts">
import { reactive } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/authStore'
import LogoIcon from '@/shared/components/common/LogoIcon.vue'

const router = useRouter()
const authStore = useAuthStore()

const form = reactive({
  username: '',
  password: '',
})

async function handleLogin() {
  try {
    await authStore.login(form)
    router.push({ name: 'home' })
  } catch {
    // error is stored in authStore.error
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

      <t-alert v-if="authStore.error" theme="error" :message="authStore.error" style="margin-bottom: 16px" />

      <t-form @submit="handleLogin" class="login-form" label-width="0">
        <t-form-item>
          <t-input
            v-model="form.username"
            placeholder="Username"
            size="large"
            clearable
          />
        </t-form-item>
        <t-form-item>
          <t-input
            v-model="form.password"
            type="password"
            placeholder="Password"
            size="large"
          />
        </t-form-item>
        <t-form-item>
          <t-button
            theme="primary"
            type="submit"
            block
            size="large"
            shape="round"
            :loading="authStore.loading"
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
  padding: 20px;
}

.login-card {
  width: 100%;
  max-width: 400px;
  padding: 32px;
}

.login-logo {
  text-align: center;
  margin-bottom: 24px;
  color: var(--text-primary);
}

.login-title {
  font-size: var(--font-2xl);
  font-weight: 700;
  text-align: center;
  margin-bottom: 32px;
  color: var(--text-primary);
}

.login-form :deep(.t-form__item) {
  margin-bottom: 16px;
}

.login-form :deep(.t-form__controls) {
  margin-left: 0 !important;
}

.login-form :deep(.t-input) {
  background: var(--bg-secondary) !important;
  border-radius: var(--radius-md) !important;
  padding: 0 12px !important;
}

.login-form :deep(.t-button--theme-primary) {
  background: linear-gradient(135deg, #86e01e, #67b90c) !important;
  border: none !important;
  color: #000 !important;
  font-weight: 700 !important;
}

.login-footer {
  text-align: center;
  margin-top: 24px;
  color: var(--text-secondary);
  font-size: var(--font-sm);
}

.login-link {
  color: var(--accent-blue);
  margin-left: 4px;
}
</style>
