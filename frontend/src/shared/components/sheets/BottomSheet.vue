<script setup lang="ts">
/**
 * Sheet / drawer.
 *
 * Phone (< 700px): a bottom sheet that tracks the finger 1:1 from its
 * header, rubber-bands when pulled up, and on release projects the flick's
 * momentum to decide dismiss vs. snap back, handing the release velocity to
 * a spring. It can be grabbed again mid-animation (the spring stops at its
 * presentation value). Wider screens: a centred modal panel that
 * materialises (scale + backdrop blur + opacity) instead of sliding.
 *
 * Reduced motion: short cross-fade, no movement.
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import {
  createSpring, project, rubberband, velocityFrom, prefersReducedMotion,
  SPRING_DEFAULT, SPRING_EXIT, SPRING_SHEET_FLICK, type Spring,
} from '@/shared/motion/spring'

const props = withDefaults(defineProps<{
  modelValue: boolean
  title?: string
  /** Accessible name when there is no visible title. */
  label?: string
  /** 'auto' hugs content; 'large' is a tall page-style sheet on phones. */
  size?: 'auto' | 'large'
}>(), { title: '', label: '', size: 'auto' })

const emit = defineEmits<{ 'update:modelValue': [open: boolean] }>()

const mounted = ref(false)
const panel = ref<HTMLElement | null>(null)
const scrim = ref<HTMLElement | null>(null)
const titleId = `sheet-title-${Math.random().toString(36).slice(2, 8)}`
const isPhone = () => window.matchMedia('(max-width: 43.74rem)').matches

let spring: Spring | null = null
let mode: 'sheet' | 'modal' = 'sheet'
let height = 0
let reduced = false
let lastFocus: HTMLElement | null = null

const ariaLabel = computed(() => (props.title ? undefined : props.label || undefined))

/* ---- rendering: one number drives everything, so any interruption is
   continuous. Sheet: y in px (0 = open). Modal: progress 0..1. ---------- */
function render(v: number) {
  const el = panel.value
  const sc = scrim.value
  if (!el || !sc) return
  if (reduced) {
    el.style.opacity = String(v)
    sc.style.opacity = String(v)
    return
  }
  if (mode === 'sheet') {
    const p = height ? Math.min(1, Math.max(0, 1 - v / height)) : 1
    el.style.transform = `translate3d(0, ${v}px, 0)`
    sc.style.opacity = String(p)
  } else {
    const p = Math.max(0, v)
    el.style.opacity = String(Math.min(1, p))
    el.style.transform = `scale(${0.96 + 0.04 * p})`
    // Materialise: the glass thickens as it arrives.
    const blur = `blur(${Math.round(40 * Math.min(1, p))}px) saturate(${100 + 80 * Math.min(1, p)}%)`
    el.style.backdropFilter = blur
    el.style.setProperty('-webkit-backdrop-filter', blur)
    sc.style.opacity = String(Math.min(1, p))
  }
}

async function open() {
  lastFocus = document.activeElement as HTMLElement | null
  mounted.value = true
  await nextTick()
  const el = panel.value
  if (!el) return
  reduced = prefersReducedMotion()
  mode = isPhone() ? 'sheet' : 'modal'
  height = el.getBoundingClientRect().height
  document.documentElement.classList.add('has-sheet')

  if (reduced) {
    spring = createSpring(0, render)
    render(0)
  } else if (mode === 'sheet') {
    spring = spring && spring.running ? spring : createSpring(height, render)
    render(spring.value)
  } else {
    spring = spring && spring.running ? spring : createSpring(0, render)
    render(spring.value)
  }
  focusInitial()
  if (reduced) {
    // gentle cross-fade instead of movement
    await fade(1)
  } else {
    await spring.to(mode === 'sheet' ? 0 : 1, SPRING_DEFAULT)
  }
}

let exitVelocity: number | undefined

async function close() {
  const velocity = exitVelocity
  exitVelocity = undefined
  if (!spring) { finishClose(); return }
  let done: boolean
  if (reduced) {
    done = await fade(0)
  } else if (mode === 'sheet') {
    done = await spring.to(height + 24, SPRING_EXIT, velocity)
  } else {
    done = await spring.to(0, SPRING_EXIT)
  }
  // `done` is false when re-opened mid-exit — keep the sheet then.
  if (done && !props.modelValue) finishClose()
}

function finishClose() {
  mounted.value = false
  spring?.stop()
  spring = null
  document.documentElement.classList.remove('has-sheet')
  lastFocus?.focus?.({ preventScroll: true })
}

function fade(to: number): Promise<boolean> {
  const el = panel.value
  const sc = scrim.value
  if (!el || !sc) return Promise.resolve(true)
  el.style.transition = sc.style.transition = 'opacity 200ms ease'
  requestAnimationFrame(() => render(to))
  return new Promise((res) => setTimeout(() => res(props.modelValue === (to === 1)), 210))
}

watch(() => props.modelValue, (v) => { if (v) open(); else close() }, { immediate: true })

function requestClose(velocity?: number) {
  if (!props.modelValue) return
  // Hand the flick's velocity to the exit spring: no seam between the drag
  // and the animation.
  exitVelocity = velocity
  emit('update:modelValue', false)
}

/* ---- drag (sheet mode only) ------------------------------------------ */
let dragging = false
let pending = false
let pointerId = -1
let startY = 0
let startValue = 0
let history: Array<[number, number]> = []

function onPointerDown(e: PointerEvent) {
  if (mode !== 'sheet' || reduced || !spring || e.button !== 0) return
  if (pending || dragging) return // ignore extra fingers once engaged
  pending = true
  pointerId = e.pointerId
  startY = e.clientY
  spring.stop() // grab it mid-flight: continue from the presentation value
  startValue = spring.value
  history = [[e.timeStamp, startValue]]
}

function onPointerMove(e: PointerEvent) {
  if (e.pointerId !== pointerId || (!pending && !dragging) || !spring) return
  const dy = e.clientY - startY
  if (pending) {
    // ~10px hysteresis before committing to a drag, so taps stay taps.
    if (Math.abs(dy) < 10) return
    pending = false
    dragging = true
    ;(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId)
    // Start tracking from here so the sheet doesn't jump by the threshold;
    // from now on it moves 1:1 with the finger, keeping the grab offset.
    startY = e.clientY
  }
  let y = startValue + (e.clientY - startY)
  if (y < 0) y = -rubberband(-y, height)
  spring.set(y)
  history.push([e.timeStamp, y])
  if (history.length > 20) history.shift()
}

function onPointerUp(e: PointerEvent) {
  if (e.pointerId !== pointerId) return
  const wasDragging = dragging
  dragging = false
  pending = false
  pointerId = -1
  if (!spring) return
  if (!wasDragging) {
    // a tap on the header: if we had interrupted an animation, resume it
    if (spring.value !== 0) spring.to(props.modelValue ? 0 : height + 24, SPRING_DEFAULT)
    return
  }
  const v = velocityFrom(history)
  const projected = spring.value + project(v)
  // Decide by where the gesture is going, not where it let go.
  if (projected > height * 0.5 || v > 900) {
    requestClose(v)
  } else {
    spring.to(0, SPRING_SHEET_FLICK, v)
  }
}

/* ---- focus & keyboard -------------------------------------------------- */
function focusInitial() {
  const el = panel.value
  if (!el) return
  const auto = el.querySelector<HTMLElement>('[autofocus], textarea, input:not([type=hidden])')
  ;(auto ?? el).focus({ preventScroll: true })
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.stopPropagation()
    requestClose()
    return
  }
  if (e.key !== 'Tab' || !panel.value) return
  const nodes = Array.from(panel.value.querySelectorAll<HTMLElement>(
    'a[href], button:not([disabled]), textarea, input, select, [tabindex]:not([tabindex="-1"])',
  )).filter(n => n.offsetParent !== null)
  if (!nodes.length) return
  const first = nodes[0]!
  const last = nodes[nodes.length - 1]!
  if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
  else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
}

onBeforeUnmount(() => {
  spring?.stop()
  document.documentElement.classList.remove('has-sheet')
})
</script>

<template>
  <Teleport to="body">
    <div v-if="mounted" class="sheet-root" @keydown="onKeydown">
      <div ref="scrim" class="sheet-scrim" aria-hidden="true" @click="requestClose()" />
      <section
        ref="panel"
        class="sheet"
        :class="[`sheet--${size}`]"
        role="dialog"
        aria-modal="true"
        :aria-labelledby="title ? titleId : undefined"
        :aria-label="ariaLabel"
        tabindex="-1"
      >
        <header
          class="sheet__header"
          @pointerdown="onPointerDown"
          @pointermove="onPointerMove"
          @pointerup="onPointerUp"
          @pointercancel="onPointerUp"
        >
          <span class="sheet__grabber" aria-hidden="true" />
          <div class="sheet__bar">
            <slot name="leading">
              <button type="button" class="sheet__close" aria-label="关闭" @click="requestClose()">
                <t-icon name="close" :size="18" />
              </button>
            </slot>
            <h2 v-if="title" :id="titleId" class="sheet__title">{{ title }}</h2>
            <div class="sheet__trailing"><slot name="trailing" /></div>
          </div>
        </header>
        <div class="sheet__body">
          <slot />
        </div>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.sheet-root {
  position: fixed;
  inset: 0;
  z-index: var(--z-sheet);
  display: flex;
  align-items: flex-end;
  justify-content: center;
}

/* Dim to focus: a modal task gets a scrim; its opacity is driven by the
   sheet's position so dragging the sheet down gradually reveals the page. */
.sheet-scrim {
  position: absolute;
  inset: 0;
  background: var(--scrim);
  opacity: 0;
}

.sheet {
  position: relative;
  display: flex;
  flex-direction: column;
  width: 100%;
  max-height: calc(100dvh - var(--safe-top) - var(--space-3));
  background: var(--material-thick);
  -webkit-backdrop-filter: var(--material-blur-thick);
  backdrop-filter: var(--material-blur-thick);
  border-radius: var(--radius-sheet) var(--radius-sheet) 0 0;
  box-shadow: var(--shadow-sheet), inset 0 var(--hairline) 0 var(--material-edge);
  color: var(--color-label);
  outline: none;
  transform: translate3d(0, 100%, 0);
  will-change: transform;
}

.sheet--large {
  height: calc(100dvh - var(--safe-top) - var(--space-3));
}

.sheet__header {
  flex-shrink: 0;
  padding-top: var(--space-2);
  /* the header is the drag handle: the sheet owns every axis here */
  touch-action: none;
  cursor: grab;
  -webkit-user-select: none;
  user-select: none;
}

.sheet__header:active {
  cursor: grabbing;
}

.sheet__grabber {
  display: block;
  width: 2.25rem;
  height: 5px;
  margin: 0 auto var(--space-1);
  border-radius: var(--radius-full);
  background: var(--color-label-quaternary);
}

.sheet__bar {
  display: grid;
  grid-template-columns: minmax(var(--hit-target), 1fr) auto minmax(var(--hit-target), 1fr);
  align-items: center;
  min-height: var(--hit-target);
  padding: 0 var(--space-3);
}

.sheet__title {
  grid-column: 2;
  font-size: var(--text-headline);
  line-height: var(--leading-headline);
  letter-spacing: var(--tracking-headline);
  font-weight: var(--weight-semibold);
  text-align: center;
}

.sheet__trailing {
  grid-column: 3;
  justify-self: end;
  display: flex;
  align-items: center;
}

.sheet__close {
  grid-column: 1;
  justify-self: start;
  display: grid;
  place-items: center;
  width: 1.875rem;
  height: 1.875rem;
  border-radius: var(--radius-full);
  background: var(--color-fill);
  color: var(--color-label-secondary);
  transition: transform var(--duration-release) var(--ease-spring), background-color var(--duration-fast) var(--ease-out);
}

/* hit area stays 44pt even though the visible chip is 30pt */
.sheet__close::after {
  content: '';
  position: absolute;
  width: var(--hit-target);
  height: var(--hit-target);
}

.sheet__close:active {
  transform: scale(var(--press-scale-small));
  background: var(--color-fill-strong);
  transition-duration: var(--duration-press);
  transition-timing-function: var(--ease-out);
}

.sheet__body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: 0 var(--space-4) calc(var(--space-4) + var(--safe-bottom));
}

/* ---- Wider screens: centred modal panel -------------------------------- */
@media (min-width: 43.75rem) {
  .sheet-root {
    align-items: center;
    padding: var(--space-6);
  }

  .sheet {
    width: min(35rem, 100%);
    max-height: min(44rem, calc(100dvh - var(--space-12)));
    border-radius: var(--radius-xl);
    box-shadow: var(--shadow-popover), inset 0 var(--hairline) 0 var(--material-edge);
    transform: scale(0.96);
    opacity: 0;
    will-change: transform, opacity;
  }

  .sheet--large {
    height: auto;
  }

  .sheet__header {
    cursor: default;
    touch-action: auto;
    padding-top: var(--space-2);
  }

  .sheet__grabber {
    display: none;
  }

  .sheet__body {
    padding-bottom: var(--space-5);
  }
}

@media (prefers-reduced-motion: reduce) {
  .sheet {
    transform: none;
    opacity: 0;
  }
}
</style>
