<script setup lang="ts">
defineProps<{ title: string; mono?: boolean }>()
</script>

<template>
  <li class="admin-row">
    <div class="admin-row__main">
      <strong class="admin-row__title" :class="{ 'admin-row__title--mono': mono }">{{ title }}</strong>
      <div v-if="$slots.meta" class="admin-row__meta"><slot name="meta" /></div>
    </div>
    <div v-if="$slots.default" class="admin-row__actions"><slot /></div>
  </li>
</template>
