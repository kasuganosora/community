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
      password: form.password,
      passwordConfirm: form.passwordConfirm,
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

.register-form :deep(.t-form__controls--content) {
  display: block !important;
}

.register-form :deep(.t-input__tips) {
  color: var(--color-label-secondary) !important;
}

.input-prefix {
  color: var(--color-label-secondary);
}

.input-suffix {
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
  font-variant-numeric: tabular-nums;
}

.form-error {
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
