<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'

const router = useRouter()
const query = ref('')

function handleSearch() {
  const trimmedQuery = query.value.trim()
  if (trimmedQuery) {
    router.push({ name: 'search', query: { q: trimmedQuery } }).catch(() => {})
  }
}
</script>

<template>
  <div class="search-box">
    <t-input
      v-model="query"
      placeholder="Search"
      aria-label="Search"
      clearable
      @enter="handleSearch"
    >
      <template #prefixIcon>
        <t-icon name="search" />
      </template>
    </t-input>
  </div>
</template>

<style scoped>
.search-box {
  position: sticky;
  top: 0;
  padding: var(--space-2) 0;
  background: var(--bg-primary);
  z-index: 10;
}
</style>
