<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'

const router = useRouter()
const query = ref('')

function handleSearch() {
  if (query.value.trim()) {
    router.push({ name: 'search', query: { q: query.value.trim() } })
  }
}
</script>

<template>
  <div class="search-box">
    <t-input
      v-model="query"
      placeholder="Search"
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
  padding: 8px 0;
  background: var(--bg-primary);
  z-index: 10;
}
</style>
