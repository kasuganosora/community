<script setup lang="ts">
import { useRouter } from 'vue-router'

const router = useRouter()

function goHome() {
  // Catch to avoid unhandled rejection if navigation is cancelled/redirected
  router.push({ name: 'home' }).catch(() => {})
}
</script>

<template>
  <div class="not-found-page">
    <div class="not-found__content">
      <h1 class="not-found__code">404</h1>
      <p class="not-found__text">这个页面不存在。</p>
      <button class="btn btn--primary" @click="goHome">
        回到首页
      </button>
    </div>
  </div>
</template>

<style scoped>
.not-found-page {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100dvh;
  padding: var(--space-8) var(--gutter);
  background: var(--color-bg);
}

.not-found__content {
  text-align: center;
}

.not-found__code {
  font-family: var(--font-rounded);
  font-size: 5rem;
  line-height: 1;
  letter-spacing: -0.04em;
  font-weight: var(--weight-bold);
  color: var(--color-label);
  font-variant-numeric: tabular-nums;
  margin-bottom: var(--space-3);
}

.not-found__text {
  color: var(--color-label-secondary);
  font-size: var(--text-body);
  margin-bottom: var(--space-6);
}
</style>
