<script setup lang="ts" generic="T extends string">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

/**
 * One indicator, not one per tab. It travels on transform, so a second tap
 * while it is still moving retargets from wherever it is — the selection
 * doesn't disappear and grow back at the new tab.
 */
const props = defineProps<{
  modelValue: T
  options: { value: T; label: string; to?: string }[]
  label?: string
}>()

const emit = defineEmits<{ 'update:modelValue': [value: T] }>()

const router = useRouter()
const bar = ref<HTMLElement | null>(null)
const x = ref(0)
const ready = ref(false)

const index = computed(() => Math.max(0, props.options.findIndex((o) => o.value === props.modelValue)))

function place() {
  const root = bar.value
  if (!root) return
  const current = root.querySelector<HTMLElement>('[aria-selected="true"]')
  const mark = root.querySelector<HTMLElement>('.tab-bar__indicator')
  if (!current || !mark) return
  const rootRect = root.getBoundingClientRect()
  const tabRect = current.getBoundingClientRect()
  const width = mark.offsetWidth
  x.value = tabRect.left - rootRect.left + (tabRect.width - width) / 2
}

let observer: ResizeObserver | null = null

onMounted(async () => {
  await nextTick()
  place()
  requestAnimationFrame(() => { ready.value = true })
  if (typeof ResizeObserver !== 'undefined' && bar.value) {
    observer = new ResizeObserver(() => place())
    observer.observe(bar.value)
  }
})

onBeforeUnmount(() => observer?.disconnect())

watch(() => [props.modelValue, props.options.length] as const, () => nextTick(place))

function select(value: T) {
  const opt = props.options.find((o) => o.value === value)
  if (!opt || opt.value === props.modelValue) return
  if (opt.to) router.replace(opt.to).catch(() => {})
  else emit('update:modelValue', value)
}

function onKeydown(e: KeyboardEvent) {
  const dir = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0
  if (!dir || !props.options.length) return
  e.preventDefault()
  const next = props.options[(index.value + dir + props.options.length) % props.options.length]
  if (!next) return
  select(next.value)
  nextTick(() => bar.value?.querySelector<HTMLElement>('[aria-selected="true"]')?.focus())
}
</script>

<template>
  <div
    ref="bar"
    class="tab-bar"
    role="tablist"
    :aria-label="label"
    @keydown="onKeydown"
  >
    <template v-for="opt in options" :key="opt.value">
      <router-link
        v-if="opt.to"
        :to="opt.to"
        replace
        role="tab"
        class="tab-bar__item"
        :class="{ 'tab-bar__item--active': opt.value === modelValue }"
        :aria-selected="opt.value === modelValue"
        :tabindex="opt.value === modelValue ? 0 : -1"
      >
        <span class="tab-bar__text">{{ opt.label }}</span>
      </router-link>
      <button
        v-else
        type="button"
        role="tab"
        class="tab-bar__item"
        :class="{ 'tab-bar__item--active': opt.value === modelValue }"
        :aria-selected="opt.value === modelValue"
        :tabindex="opt.value === modelValue ? 0 : -1"
        @click="select(opt.value)"
      >
        <span class="tab-bar__text">{{ opt.label }}</span>
      </button>
    </template>
    <div
      class="tab-bar__indicator"
      :class="{ 'tab-bar__indicator--ready': ready }"
      :style="{ transform: `translate3d(${x}px, 0, 0)` }"
      aria-hidden="true"
    />
  </div>
</template>
