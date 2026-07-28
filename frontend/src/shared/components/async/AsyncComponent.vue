<script setup lang="ts">
import { shallowRef, ref, onMounted } from 'vue'
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

const isLoading = defineModel<boolean>('loading', { default: true })
const hasError = defineModel<boolean>('error', { default: false })
const LoadedComponent = shallowRef<unknown>(null)
const errorValue = ref<Error | null>(null)

async function load() {
  isLoading.value = true
  hasError.value = false
  errorValue.value = null

  // Minimum loading time for better UX
  const minDelay = new Promise((resolve) => setTimeout(resolve, props.delay))

  try {
    const componentPromise = props.loader()

    // Race between load and timeout
    const loaded = await Promise.race([
      componentPromise,
      new Promise((_, reject) =>
        setTimeout(() => reject(new Error('Component load timeout')), props.timeout)
      ),
    ])

    await minDelay // Ensure minimum loading time

    LoadedComponent.value = loaded
  } catch (err) {
    errorValue.value = err instanceof Error ? err : new Error(String(err))
    hasError.value = true
  } finally {
    isLoading.value = false
  }
}

onMounted(load)

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
          <p>Failed to load component</p>
          <t-button theme="primary" @click="retry">Retry</t-button>
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
