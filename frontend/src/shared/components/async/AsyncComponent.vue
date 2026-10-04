<script setup lang="ts">
import { shallowRef, ref, onMounted, onUnmounted } from 'vue'
import FadeTransition from '@/shared/components/transitions/FadeTransition.vue'

interface Props {
  loader: () => Promise<unknown>
  delay?: number
  timeout?: number
}

const props = withDefaults(defineProps<Props>(), {
  delay: 200,
  timeout: 10000,
})

// Disable automatic attribute inheritance: $attrs are explicitly forwarded
// to the loaded component via v-bind="$attrs" in the template. Without this,
// fallthrough attributes would also be applied to the root <FadeTransition>,
// causing duplicate binding.
defineOptions({ inheritAttrs: false })

const isLoading = defineModel<boolean>('loading', { default: true })
const hasError = defineModel<boolean>('error', { default: false })
const LoadedComponent = shallowRef<unknown>(null)
const errorValue = ref<Error | null>(null)

// Track timers so they can be cleaned up on unmount / re-entry
let minDelayTimer: ReturnType<typeof setTimeout> | null = null
let timeoutTimer: ReturnType<typeof setTimeout> | null = null
// Resolve handle for minDelay — allows cancellation to settle the
// promise instead of leaving it pending forever (which would leak
// the async frame and skip the finally block).
let resolveMinDelay: (() => void) | null = null

// Guard against applying a stale result after retry() or unmount
let loadVersion = 0
let cancelled = false

function clearTimers() {
  if (minDelayTimer !== null) {
    clearTimeout(minDelayTimer)
    minDelayTimer = null
  }
  if (resolveMinDelay) {
    resolveMinDelay()
    resolveMinDelay = null
  }
  if (timeoutTimer !== null) {
    clearTimeout(timeoutTimer)
    timeoutTimer = null
  }
}

async function load() {
  // Cancel any in-flight load: a previous invocation still awaiting will
  // see version !== loadVersion and bail out before mutating state.
  clearTimers()
  const version = ++loadVersion

  isLoading.value = true
  hasError.value = false
  errorValue.value = null

  // Minimum loading time for better UX
  const minDelay = new Promise<void>((resolve) => {
    resolveMinDelay = resolve
    minDelayTimer = setTimeout(resolve, props.delay)
  })

  try {
    const componentPromise = props.loader()

    // Race between load and timeout
    const loaded = await Promise.race([
      componentPromise,
      new Promise((_, reject) => {
        timeoutTimer = setTimeout(() => reject(new Error('Component load timeout')), props.timeout)
      }),
    ])

    await minDelay // Ensure minimum loading time

    // Ignore result if a newer load() started or component unmounted
    if (cancelled || version !== loadVersion) return

    LoadedComponent.value = loaded
  } catch (err) {
    if (cancelled || version !== loadVersion) return
    errorValue.value = err instanceof Error ? err : new Error(String(err))
    hasError.value = true
  } finally {
    if (!cancelled && version === loadVersion) {
      isLoading.value = false
    }
    if (version === loadVersion) {
      clearTimers()
    }
  }
}

onMounted(load)

onUnmounted(() => {
  cancelled = true
  clearTimers()
})

function retry() {
  load()
}

</script>


<template>
  <FadeTransition>
    <div v-if="isLoading" class="async-loading" key="loading">
      <slot name="loading">
        <div class="async-loading__spinner">
          <t-loading size="large" />
        </div>
      </slot>
    </div>

    <div v-else-if="hasError" class="async-error" key="error">
      <slot name="error" :error="errorValue" :retry="retry">
        <div class="async-error__content">
          <t-icon name="error-circle" size="48" />
          <p>页面加载失败</p>
          <t-button theme="primary" @click="retry">重试</t-button>
        </div>
      </slot>
    </div>

    <component v-else :is="LoadedComponent" key="content" v-bind="$attrs" />
  </FadeTransition>
</template>


<style scoped>
.async-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 200px;
}

.async-loading__spinner {
  padding: var(--space-8);
}

.async-error {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 200px;
  padding: var(--space-8);
}

.async-error__content {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-4);
  color: var(--text-secondary);
}
</style>
