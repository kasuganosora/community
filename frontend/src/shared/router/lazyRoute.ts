import { defineComponent, h, onBeforeUnmount, onMounted, ref, shallowRef, type Component, type ShallowRef } from 'vue'
import { useRoute, type RouteLocationNormalized } from 'vue-router'
import RoutePending from './RoutePending.vue'

type Loader = () => Promise<{ default: Component }>
const preloaders = new WeakMap<object, () => void>()

/** Start parent layouts and child pages together, after permission guards pass.
 * Never await these imports in a navigation guard. */
export function preloadRouteViews(to: RouteLocationNormalized) {
  for (const record of to.matched) {
    for (const component of Object.values(record.components ?? {})) {
      if (component) preloaders.get(component)?.()
    }
  }
}

/** Keep code splitting without making the router await a page's entire module
 * graph. The wrapper commits first; result/data loading stays on that page. */
export function lazyRoute(loader: Loader, heading: (name: unknown) => string = () => 'Community'): Component {
  let cached: Component | null = null
  let pending: Promise<Component> | null = null
  function load() {
    if (!pending) pending = loader().then(module => {
      cached = module.default
      return cached
    }).finally(() => { pending = null })
    return pending
  }

  const page = defineComponent({
    name: 'LazyRoute',
    inheritAttrs: false,
    setup(_, { attrs, slots }) {
      const route = useRoute()
      const component: ShallowRef<Component | null> = shallowRef(cached)
      const failed = ref(false)
      let active = true
      onMounted(async () => {
        if (component.value) return
        try {
          const loaded = await load()
          if (active) component.value = loaded
        } catch {
          if (active) failed.value = true
        }
      })
      onBeforeUnmount(() => { active = false })
      // Descendants keep RouterView's route injection, including composition
      // leave/update guards. Route props and slots must reach the actual page.
      return () => component.value
        ? h(component.value, attrs, slots)
        : h(RoutePending, { title: heading(route.name), failed: failed.value })
    },
  })
  preloaders.set(page, () => { if (!cached) void load().catch(() => {}) })
  return page
}
