<script setup lang="ts">
import { useRouter } from 'vue-router'

const router = useRouter()
</script>

<template>
  <div class="not-found-page">
    <div class="not-found__content">
      <h1 class="not-found__code">404</h1>
      <p class="not-found__text">This page could not be found.</p>
      <button class="btn btn--primary" @click="router.push({ name: 'home' })">
        Back to Home
      </button>
    </div>
  </div>
</template>

<style scoped>
.not-found-page {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  background: var(--bg-primary);
}

.not-found__content {
  text-align: center;
}

.not-found__code {
  font-size: 6rem;
  font-weight: 800;
  color: var(--text-primary);
  margin-bottom: var(--space-2);
}

.not-found__text {
  color: var(--text-secondary);
  margin-bottom: var(--space-4);
}
</style>
