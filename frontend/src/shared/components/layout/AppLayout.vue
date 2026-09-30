<script setup lang="ts">
import LeftSidebar from './LeftSidebar.vue'
import RightSidebar from './RightSidebar.vue'
import MobileTabBar from './MobileTabBar.vue'
import ComposeSheet from '@/shared/components/sheets/ComposeSheet.vue'
import AccountSheet from '@/shared/components/sheets/AccountSheet.vue'
import { useUiChrome } from '@/shared/composables/useUiChrome'
import { useScrollEdge } from '@/shared/composables/useScrollEdge'

const { openCompose } = useUiChrome()
useScrollEdge()
</script>

<template>
  <div class="app-layout">
    <aside class="app-layout__left">
      <LeftSidebar />
    </aside>
    <main class="app-layout__center">
      <!-- New page fades up a few px; the old one leaves instantly, so
           navigation never waits on an exit animation. -->
      <router-view v-slot="{ Component }">
        <Transition name="page" mode="out-in">
          <component :is="Component" />
        </Transition>
      </router-view>
    </main>
    <aside class="app-layout__right">
      <RightSidebar />
    </aside>

    <!-- Mobile chrome: floating compose button + translucent tab bar -->
    <button
      class="app-fab"
      type="button"
      aria-label="发布新帖子"
      data-shot="compose"
      @click="openCompose"
    >
      <t-icon name="edit" :size="24" />
    </button>
    <MobileTabBar class="app-layout__tabbar" />

    <ComposeSheet />
    <AccountSheet />
  </div>
</template>

<style scoped>
/* Three columns on wide screens; the rails collapse progressively so the
   reading column never shrinks below its measure. */
.app-layout {
  display: flex;
  justify-content: center;
  max-width: 80rem;
  margin: 0 auto;
  min-height: 100vh;
  min-height: 100dvh;
}

.app-layout__left {
  width: var(--sidebar-left-width);
  flex-shrink: 0;
  position: sticky;
  top: 0;
  height: 100vh;
  height: 100dvh;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: none;
}

.app-layout__center {
  flex: 1 1 auto;
  width: 100%;
  max-width: var(--content-max-width);
  min-width: 0;
  min-height: 100vh;
  min-height: 100dvh;
  box-shadow:
    calc(-1 * var(--hairline)) 0 0 var(--color-separator),
    var(--hairline) 0 0 var(--color-separator);
}

.app-layout__right {
  width: var(--sidebar-right-width);
  flex-shrink: 0;
  position: sticky;
  top: 0;
  height: 100vh;
  height: 100dvh;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: none;
  padding: 0 var(--space-6);
}

.app-fab,
.app-layout__tabbar {
  display: none;
}

@media (max-width: 74.99rem) {
  .app-layout__left {
    width: var(--sidebar-left-collapsed);
  }
}

@media (max-width: 62.49rem) {
  .app-layout__right {
    display: none;
  }
}

/* ---- Phone: content edge to edge, navigation moves to the thumb zone ---- */
@media (max-width: 43.74rem) {
  .app-layout__left {
    display: none;
  }

  .app-layout__center {
    max-width: none;
    box-shadow: none;
    /* keep the last post clear of the tab bar + home indicator */
    padding-bottom: calc(var(--tabbar-height) + var(--safe-bottom) + var(--space-4));
  }

  .app-layout__tabbar {
    display: flex;
  }

  .app-fab {
    position: fixed;
    right: var(--space-4);
    bottom: calc(var(--tabbar-height) + var(--safe-bottom) + var(--space-4));
    z-index: var(--z-fab);
    display: grid;
    place-items: center;
    width: 3.5rem;
    height: 3.5rem;
    border-radius: var(--radius-full);
    background: var(--color-accent);
    color: var(--color-on-accent);
    box-shadow: var(--shadow-fab);
    transition: transform var(--duration-release) var(--ease-spring);
  }

  .app-fab:active {
    transform: scale(var(--press-scale));
    transition-duration: var(--duration-press);
    transition-timing-function: var(--ease-out);
  }
}
</style>
