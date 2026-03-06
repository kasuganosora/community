<script setup lang="ts">
</script>

<template>
  <div class="messages-page">
    <div class="page-header">
      <h1 class="page-header__title">Messages</h1>
    </div>
    <div class="empty-state">
      <div class="empty-state__icon">
        <t-icon name="mail" :size="48" />
      </div>
      <h2 class="empty-state__title">Welcome to your inbox!</h2>
      <p class="empty-state__text">
        Drop a line, share posts and more with private conversations between you and others.
      </p>
      <t-button theme="primary" shape="round" size="large">
        Write a message
      </t-button>
    </div>
  </div>
</template>

<style scoped>
.messages-page {
  min-height: 100vh;
}
</style>
