<script setup lang="ts">
import LeftSidebar from './LeftSidebar.vue'
import RightSidebar from './RightSidebar.vue'
import MobileTabBar from './MobileTabBar.vue'
import ComposeSheet from '@/shared/components/sheets/ComposeSheet.vue'
import AccountSheet from '@/shared/components/sheets/AccountSheet.vue'
import { useUiChrome } from '@/shared/composables/useUiChrome'
import { useScrollEdge } from '@/shared/composables/useScrollEdge'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useKeyboardInset } from '@/shared/composables/useMobileDock'

const { openCompose } = useUiChrome()
useScrollEdge()

// On a profile the header's follow/edit button sits where the FAB floats on
// narrow screens; keep the FAB out of the way until the header scrolls off.
const PROFILE_ROUTES = new Set(['profile', 'followers', 'following'])
const route = useRoute()
const isGroups = computed(() => route.path === '/groups' || route.path.startsWith('/groups/'))
const isSettings = computed(() => route.path === '/settings' || route.path.startsWith('/settings/'))
const scrollY = ref(typeof window === 'undefined' ? 0 : window.scrollY)
// Only the profile FAB rule needs scrollY, and only whether it is past 600px:
// rAF-throttle and write the ref only when that answer changes.
let scrollFrame = 0
const readScroll = () => { scrollFrame = 0; const y = window.scrollY; if ((y < 600) !== (scrollY.value < 600)) scrollY.value = y }
const onScroll = () => { if (!scrollFrame) scrollFrame = requestAnimationFrame(readScroll) }
onMounted(() => window.addEventListener('scroll', onScroll, { passive: true }))
onBeforeUnmount(() => { window.removeEventListener('scroll', onScroll); if (scrollFrame) cancelAnimationFrame(scrollFrame) })
watch(() => route.fullPath, () => { scrollY.value = window.scrollY })
const fabHidden = computed(() => {
  const name = String(route.name)
  return isSettings.value || isGroups.value || (name === 'search' && route.query.type === 'groups') || name === 'messages' || name === 'chat-conversation' || (PROFILE_ROUTES.has(name) && scrollY.value < 600)
})

useKeyboardInset()
</script>

<template>
  <div class="app-layout" :class="{ 'app-layout--settings': isSettings, 'app-layout--groups': isGroups }">
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
    <aside v-if="!isSettings && !isGroups" class="app-layout__right">
      <RightSidebar />
    </aside>

    <!-- Mobile chrome: floating compose button + translucent tab bar -->
    <button
      class="app-fab"
      :class="{ 'app-fab--hidden': fabHidden }"
      type="button"
      :tabindex="fabHidden ? -1 : undefined"
      :aria-hidden="fabHidden ? 'true' : undefined"
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

.app-layout--groups .app-layout__center,
.app-layout--settings .app-layout__center {
  max-width: 64rem;
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
    /* the document scrolls; keep the last item clear of the fixed tab bar */
    min-height: 0;
    padding-bottom: calc(var(--tabbar-h) + var(--space-4));
  }

  .app-layout { min-height: 0; }

  /* Pages size to their content on phones: no vh/dvh-based heights. */
  .app-layout__center > :deep(*) { min-height: 0; }

  /* Chat views add the docked composer on top of the tab bar. */
  .app-layout--groups .app-layout__center { padding-bottom: var(--tabbar-h); }

  .app-layout__tabbar {
    display: flex;
  }

  .app-fab {
    position: fixed;
    right: var(--space-4);
    bottom: calc(var(--tabbar-h) + var(--space-4));
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

  .app-fab--hidden {
    transform: scale(0.6);
    opacity: 0;
    pointer-events: none;
    transition: transform var(--duration-press) var(--ease-out), opacity var(--duration-press) var(--ease-out);
  }

  .app-fab:active {
    transform: scale(var(--press-scale));
    transition-duration: var(--duration-press);
    transition-timing-function: var(--ease-out);
  }
}
</style>
