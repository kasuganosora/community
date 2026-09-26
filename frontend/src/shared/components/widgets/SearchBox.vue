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
      placeholder="搜索"
      aria-label="搜索"
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
/* Sticks with the page nav bar: same height, same material, so the rail's
   search field and the column's title read as one floating layer. */
.search-box {
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  display: flex;
  align-items: center;
  min-height: var(--bar-height);
  margin: 0 calc(-1 * var(--space-2));
  padding: var(--space-2);
  background: var(--material-bar);
  -webkit-backdrop-filter: var(--material-blur-bar);
  backdrop-filter: var(--material-blur-bar);
}

.search-box :deep(.t-input__wrap) {
  width: 100%;
}

.search-box :deep(.t-input) {
  height: 2.375rem;
  border-radius: var(--radius-full);
  padding: 0 var(--space-4);
}
</style>
