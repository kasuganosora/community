<script setup lang="ts">
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <header class="admin-page-header">
    <div class="admin-page-header__text">
      <h1>{{ title }}</h1>
      <p v-if="description">{{ description }}</p>
    </div>
    <div v-if="$slots.default" class="admin-page-header__actions"><slot /></div>
  </header>
</template>
