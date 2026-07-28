<script setup lang="ts">
import { ref, reactive, computed } from 'vue'
import { useRouter } from 'vue-router'
import { authApi } from '../../infrastructure/authApi'

const router = useRouter()

const step = ref<'rules' | 'form'>('rules')

// Server rules
const rules = [
  { id: 1, text: 'Be friendly and respectful' },
  { id: 2, text: 'No NSFW content, especially CSAM' },
  { id: 3, text: 'No terrorism-related content' },
  { id: 4, text: 'No hate speech' },
]

const rulesAgreed = ref(false)
const tosAgreed = ref(false)
const canContinue = computed(() => rulesAgreed.value && tosAgreed.value)

// Registration form
const form = reactive({
  username: '',
  email: '',
  password: '',
  passwordConfirm: '',
})

const registering = ref(false)
const error = ref<string | null>(null)

// Server domain from environment or default
const serverDomain = computed(() => {
  const apiUrl = import.meta.env.VITE_API_URL
  if (apiUrl) {
    try {
      const url = new URL(apiUrl)
      return url.hostname
    } catch {
      return 'community.local'
    }
  }
  return 'community.local'
})

// Email validation regex
const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
const isValidEmail = computed(() => emailRegex.test(form.email))

// Password validation: at least 8 chars, one uppercase, one lowercase, one number
const passwordRegex = /^(?=.*[a-z])(?=.*[A-Z])(?=.*\d).{8,}$/
const isStrongPassword = computed(() => passwordRegex.test(form.password))

const passwordMatch = computed(() => form.password === form.passwordConfirm)

// Fixed: Check for non-empty password confirm correctly
const showPasswordMismatch = computed(() => 
  form.passwordConfirm !== '' && !passwordMatch.value
)

const canRegister = computed(() =>
  form.username.length >= 2 &&
  isValidEmail.value &&
  isStrongPassword.value &&
  passwordMatch.value &&
  !registering.value,
)

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
    await authApi.register({ 
      username: form.username, 
      email: form.email, 
      password: form.password 
    })
    router.push({ name: 'login' })
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : 'Registration failed'
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
        <button class="register-header__close" @click="close">
          <t-icon name="close" :size="20" />
        </button>
        <h1 class="register-header__title">Register</h1>
      </div>

      <!-- Step 1: Rules -->
      <div v-if="step === 'rules'" class="register-body">
        <div class="rules-icon">
          <svg viewBox="0 0 24 24" width="48" height="48" fill="var(--accent-green)">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-6-6zm-1 2l5 5h-5V4zM6 20V4h5v7h7v9H6z"/>
          </svg>
        </div>

        <p class="rules-intro">
          To register on this server, you need to review and agree to the following.
          <strong>Please read carefully, this is important.</strong>
        </p>

        <!-- Server rules -->
        <div class="rules-section">
          <div class="rules-section__header" @click="() => {}">
            <span>Server Rules</span>
            <t-icon name="chevron-up" :size="16" />
          </div>
          <div class="rules-list">
            <div v-for="rule in rules" :key="rule.id" class="rule-item">
              <span class="rule-number">{{ rule.id }}</span>
              <span class="rule-text">{{ rule.text }}</span>
            </div>
          </div>
          <label class="agree-check">
            <input type="checkbox" v-model="rulesAgreed" class="agree-check__input" />
            <span class="agree-check__label">I agree</span>
          </label>
        </div>

        <!-- ToS -->
        <div class="rules-section">
          <div class="rules-section__header">
            <span>Terms of Service</span>
            <t-icon name="chevron-up" :size="16" />
          </div>
          <div class="rules-tos">
            <a href="#" class="tos-link">
              Terms of Service
              <t-icon name="link" :size="14" />
            </a>
          </div>
          <label class="agree-check">
            <input type="checkbox" v-model="tosAgreed" class="agree-check__input" />
            <span class="agree-check__label">I agree</span>
          </label>
        </div>

        <p class="rules-note">You must agree to all items above to continue.</p>

        <div class="register-actions">
          <t-button
            theme="default"
            shape="round"
            size="large"
            @click="goBack"
          >
            Cancel
          </t-button>
          <t-button
            theme="primary"
            shape="round"
            size="large"
            :disabled="!canContinue"
            @click="proceedToForm"
            class="btn-continue"
          >
            Continue →
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
          <t-form-item label="Username" :help="'Cannot be changed later.'">
            <t-input
              v-model="form.username"
              size="large"
              placeholder="username"
            >
              <template #prefixIcon>
                <span class="input-prefix">@</span>
              </template>
              <template #suffix>
                <span class="input-suffix">@{{ serverDomain }}</span>
              </template>
            </t-input>
          </t-form-item>

          <t-form-item label="Email">
            <t-input
              v-model="form.email"
              type="text"
              size="large"
              placeholder="email@example.com"
            >
              <template #prefixIcon>
                <t-icon name="mail" />
              </template>
            </t-input>
          </t-form-item>

          <t-form-item label="Password">
            <t-input
              v-model="form.password"
              type="password"
              size="large"
              placeholder="At least 8 characters"
            >
              <template #prefixIcon>
                <t-icon name="lock-on" />
              </template>
            </t-input>
          </t-form-item>

          <t-form-item label="Confirm Password">
            <t-input
              v-model="form.passwordConfirm"
              type="password"
              size="large"
              placeholder="Enter password again"
              :status="showPasswordMismatch ? 'error' : 'default'"
            >
              <template #prefixIcon>
                <t-icon name="lock-on" />
              </template>
            </t-input>
            <div v-if="showPasswordMismatch" class="form-error">
              Passwords do not match
            </div>
          </t-form-item>

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
              Start
            </t-button>
          </div>
        </t-form>
      </div>
    </div>
  </div>
</template>

<style scoped>
.register-page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-header-blur);
  padding: var(--space-5);
}

.register-modal {
  width: 100%;
  max-width: 520px;
  background: var(--bg-secondary);
  border-radius: var(--radius-lg);
  overflow: hidden;
  max-height: 90vh;
  overflow-y: auto;
}

.register-header {
  display: flex;
  align-items: center;
  gap: var(--space-4);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--border-color);
  position: sticky;
  top: 0;
  background: var(--bg-secondary);
  z-index: 1;
}

.register-header__close {
  padding: var(--space-1);
  border-radius: var(--radius-full);
  color: var(--text-primary);
  transition: background-color var(--transition-fast);
}

.register-header__close:hover {
  background: var(--bg-hover);
}

.register-header__title {
  font-size: var(--font-lg);
  font-weight: 700;
}

.register-body {
  padding: var(--space-6) var(--space-10);
}

.rules-icon,
.form-icon {
  text-align: center;
  margin-bottom: var(--space-4);
}

.rules-intro {
  text-align: center;
  color: var(--text-secondary);
  font-size: var(--font-sm);
  margin-bottom: var(--space-6);
  line-height: 1.5;
}

.rules-intro strong {
  color: var(--accent-green);
}

.rules-section {
  background: var(--bg-primary);
  border: 1px solid var(--border-color);
  border-radius: var(--radius-md);
  margin-bottom: var(--space-4);
  overflow: hidden;
}

.rules-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-3) var(--space-4);
  font-weight: 700;
  font-size: var(--font-md);
  color: var(--text-primary);
  cursor: pointer;
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
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: var(--accent-green);
  color: #000;
  font-size: var(--font-xs);
  font-weight: 700;
  flex-shrink: 0;
}

.rule-text {
  font-size: var(--font-md);
  color: var(--text-primary);
  padding-top: 3px;
  font-weight: 700;
}

.agree-check {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  border-top: 1px solid var(--border-color);
  cursor: pointer;
}

.agree-check__input {
  width: 20px;
  height: 20px;
  accent-color: var(--accent-green);
  cursor: pointer;
}

.agree-check__label {
  font-size: var(--font-md);
  color: var(--text-primary);
}

.rules-tos {
  padding: var(--space-1) var(--space-4) var(--space-3);
}

.tos-link {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--accent-green);
  font-size: var(--font-md);
}

.rules-note {
  text-align: center;
  color: var(--text-secondary);
  font-size: var(--font-sm);
  margin-bottom: var(--space-5);
}

.register-actions {
  display: flex;
  justify-content: center;
  gap: var(--space-3);
}

.register-actions--center {
  justify-content: center;
  margin-top: var(--space-2);
}

.btn-continue {
  background: linear-gradient(135deg, #86e01e, #67b90c) !important;
  border: none !important;
  color: #000 !important;
  font-weight: 700 !important;
  min-width: 120px;
}

.btn-continue:disabled {
  opacity: 0.5 !important;
}

.register-form :deep(.t-form__label) {
  color: var(--accent-green) !important;
  font-weight: 700;
  font-size: var(--font-sm);
  margin-bottom: var(--space-1);
  padding: 0 !important;
  width: auto !important;
}

.register-form :deep(.t-form__item) {
  margin-bottom: var(--space-5);
}

.register-form :deep(.t-form__controls) {
  margin-left: 0 !important;
}

.register-form :deep(.t-form__controls--content) {
  display: block !important;
}

.register-form :deep(.t-input__tips) {
  color: var(--warning) !important;
}

.register-form :deep(.t-input) {
  background: var(--bg-primary) !important;
  border-radius: var(--radius-md) !important;
  padding: 0 var(--space-3) !important;
}

.input-prefix {
  color: var(--text-secondary);
}

.input-suffix {
  color: var(--text-secondary);
  font-size: var(--font-sm);
}

.form-error {
  margin-top: var(--space-1);
  font-size: var(--font-xs);
  color: var(--danger);
}
</style>
