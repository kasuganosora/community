<script setup lang="ts">
import LeftSidebar from './LeftSidebar.vue'
import RightSidebar from './RightSidebar.vue'
</script>

<template>
  <div class="app-layout">
    <aside class="app-layout__left">
      <LeftSidebar />
    </aside>
    <main class="app-layout__center">
      <router-view />
    </main>
    <aside class="app-layout__right">
      <RightSidebar />
    </aside>
  </div>
</template>

<style scoped>
.app-layout {
  display: flex;
  max-width: 1265px;
  margin: 0 auto;
  min-height: 100vh;
}

.app-layout__left {
  width: var(--sidebar-left-width);
  flex-shrink: 0;
  position: sticky;
  top: 0;
  height: 100vh;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
}

.app-layout__center {
  flex: 1;
  max-width: var(--content-max-width);
  border-left: 1px solid var(--border-color);
  border-right: 1px solid var(--border-color);
  min-height: 100vh;
}

.app-layout__right {
  width: var(--sidebar-right-width);
  flex-shrink: 0;
  position: sticky;
  top: 0;
  height: 100vh;
  overflow-y: auto;
  padding: 0 var(--space-4);
}

@media (max-width: 1280px) {
  .app-layout__right {
    display: none;
  }
}

@media (max-width: 1024px) {
  .app-layout__left {
    width: var(--sidebar-left-collapsed);
  }
}

@media (max-width: 500px) {
  .app-layout__left {
    display: none;
  }
}
</style>
