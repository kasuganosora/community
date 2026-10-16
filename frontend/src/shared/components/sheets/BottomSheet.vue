<script setup lang="ts">
/**
 * Sheet / drawer.
 *
 * Phone (< 700px): a bottom sheet that tracks the finger 1:1. The header
 * always drags; the body drags only when its content is already at the
 * edge the finger is pulling toward, so scrolling and dismissing never
 * fight. Pulling past the top rubber-bands. On release the flick's momentum
 * is projected to decide dismiss vs. snap back, and that velocity is handed
 * to a spring. Grabbing mid-flight continues from the presentation value.
 * Wider screens: a centred modal panel that materialises (scale + backdrop
 * blur + opacity) instead of sliding.
 *
 * Reduced motion: short cross-fade, no movement.
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import {
  createSpring, rubberband, velocityFrom, prefersReducedMotion,
  sheetDragIntent, shouldDismissSheet, SPRING_DEFAULT, SPRING_EXIT, SPRING_SHEET_FLICK, type Spring,
} from '@/shared/motion/spring'

const props = withDefaults(defineProps<{
  modelValue: boolean
  title?: string
  /** Accessible name when there is no visible title. */
  label?: string
  /** 'auto' hugs content; 'large' is a tall page-style sheet on phones. */
  size?: 'auto' | 'large'
}>(), { title: '', label: '', size: 'auto' })

const emit = defineEmits<{ 'update:modelValue': [open: boolean]; closed: [] }>()

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
/* While a drag owns the sheet, parent open/close must not start a second
   animation — the finger is the source of truth until release. */
let gestureOwns = false
let gestureGen = 0

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
  emit('closed')
  spring?.stop()
  spring = null
  document.documentElement.classList.remove('has-sheet')
  lastFocus?.focus?.({ preventScroll: true })
}

function fade(to: number): Promise<boolean> {
  const el = panel.value
  const sc = scrim.value
  if (!el || !sc) return Promise.resolve(true)
  el.style.transition = sc.style.transition = 'opacity 200ms cubic-bezier(0.23, 1, 0.32, 1)'
  requestAnimationFrame(() => render(to))
  return new Promise((res) => setTimeout(() => res(props.modelValue === (to === 1)), 210))
}

watch(() => props.modelValue, (v) => {
  if (gestureOwns) return
  if (v) void open()
  else void close()
}, { immediate: true })

function requestClose(velocity?: number) {
  // An explicit dismiss (scrim, Escape, close button) cancels a drag and
  // still has to finish, including when the parent already thinks it's closed.
  exitVelocity = velocity
  gestureGen++
  gestureOwns = false
  if (props.modelValue) emit('update:modelValue', false)
  else if (mounted.value) void close()
}

/* ---- drag (sheet mode only) ------------------------------------------ */
let dragging = false
let pending = false
let pointerId = -1
let startX = 0
let startY = 0
let startValue = 0
let history: Array<[number, number]> = []
let suppressClick = false

function isTextControl(target: EventTarget | null): boolean {
  return target instanceof Element
    && !!target.closest('input, textarea, select, [contenteditable="true"]')
}

/** The scroller this gesture should defer to, if it isn't the sheet itself. */
function contentScroller(target: EventTarget | null): HTMLElement | null {
  const root = panel.value
  if (!root) return null
  let el = target instanceof Element ? target : null
  while (el && el !== root) {
    if (el instanceof HTMLElement) {
      const oy = getComputedStyle(el).overflowY
      if ((oy === 'auto' || oy === 'scroll') && el.scrollHeight > el.clientHeight + 1) {
        return el
      }
    }
    el = el.parentElement
  }
  return root.querySelector<HTMLElement>('.sheet__body')
}

function blockTouchScroll(e: TouchEvent) {
  if (dragging) e.preventDefault()
}

function onPointerDown(e: PointerEvent) {
  if (mode !== 'sheet' || reduced || !spring || e.button !== 0) return
  if (pending || dragging) return // ignore extra fingers once engaged
  // Text controls keep their own drag (caret, selection, inner scroll).
  if (isTextControl(e.target)) return
  pending = true
  pointerId = e.pointerId
  startX = e.clientX
  startY = e.clientY
  // Don't stop the spring yet: a tap must not freeze an in-flight animation.
}

function onPointerMove(e: PointerEvent) {
  if (e.pointerId !== pointerId || (!pending && !dragging) || !spring) return
  const dx = e.clientX - startX
  const dy = e.clientY - startY
  if (pending) {
    // ~10px hysteresis before committing, so taps stay taps. The first
    // axis to win takes the gesture; a horizontal move is not a dismiss.
    if (Math.hypot(dx, dy) < 10) return
    if (Math.abs(dx) > Math.abs(dy)) {
      pending = false
      pointerId = -1
      return
    }
    const scroller = contentScroller(e.target)
    const inHeader = !!(e.target instanceof Element && (e.target as Element).closest('.sheet__header'))
    if (!inHeader && scroller) {
      const maxScroll = Math.max(0, scroller.scrollHeight - scroller.clientHeight)
      if (sheetDragIntent(dy, scroller.scrollTop, maxScroll) === 'content') {
        // This gesture belongs to the scroller. Let it go.
        pending = false
        pointerId = -1
        return
      }
    }
    pending = false
    dragging = true
    gestureOwns = true
    suppressClick = true
    // Grab mid-flight from the presentation value, then track 1:1 from
    // *here* so the hysteresis distance doesn't jump the sheet.
    spring.stop()
    startValue = spring.value
    startY = e.clientY
    history = [[e.timeStamp, e.clientY]]
    const cap = e.currentTarget as HTMLElement
    cap.setPointerCapture(e.pointerId)
    cap.addEventListener('touchmove', blockTouchScroll, { passive: false })
  }
  let y = startValue + (e.clientY - startY)
  if (y < 0) y = -rubberband(-y, height || 1)
  spring.set(y)
  // Finger velocity, not the rubber-banded sheet position — the spring
  // must continue at the speed the finger actually had.
  history.push([e.timeStamp, e.clientY])
  if (history.length > 20) history.shift()
}

function onPointerUp(e: PointerEvent) {
  if (e.pointerId !== pointerId) return
  const wasDragging = dragging
  dragging = false
  pending = false
  pointerId = -1
  panel.value?.removeEventListener('touchmove', blockTouchScroll)
  if (wasDragging) {
    // The click that follows pointerup must not activate a row. If no click
    // comes (pointercancel), don't swallow the next tap.
    requestAnimationFrame(() => { suppressClick = false })
  }
  if (!spring || !wasDragging) return
  const v = velocityFrom(history)
  void settleFromGesture(shouldDismissSheet(spring.value, v, height), v)
}

async function settleFromGesture(dismiss: boolean, velocity: number) {
  if (!spring) return
  const gen = ++gestureGen
  gestureOwns = true
  try {
    if (dismiss) {
      if (props.modelValue) emit('update:modelValue', false)
      // Critically damped on the way out: a bounce would bring the sheet
      // back on screen. The release velocity is what makes a flick feel thrown.
      const done = await spring.to(height + 24, SPRING_EXIT, velocity)
      if (gen !== gestureGen) return
      if (done && !props.modelValue) finishClose()
    } else {
      // Dragging a closing sheet back up re-opens it. The parent has to
      // hear that, or the next dismiss is a no-op and the sheet freezes.
      if (!props.modelValue) emit('update:modelValue', true)
      // Bounce only when the flick is traveling toward open. A downward
      // velocity still gets handed off, so there is no seam, but it must
      // not overshoot.
      const cfg = velocity < -300 ? SPRING_SHEET_FLICK : SPRING_DEFAULT
      await spring.to(0, cfg, velocity)
    }
  } finally {
    if (gen === gestureGen) gestureOwns = false
  }
}

function onClickCapture(e: MouseEvent) {
  if (!suppressClick) return
  suppressClick = false
  e.preventDefault()
  e.stopPropagation()
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
  panel.value?.removeEventListener('touchmove', blockTouchScroll)
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
        @pointerdown="onPointerDown"
        @pointermove="onPointerMove"
        @pointerup="onPointerUp"
        @pointercancel="onPointerUp"
        @click.capture="onClickCapture"
      >
        <header class="sheet__header">
          <span class="sheet__grabber" aria-hidden="true" />
          <div class="sheet__bar">
            <slot name="leading">
              <button type="button" class="sheet__close" :aria-label="$t('shared.BottomSheet.s1')" @click="requestClose()">
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
  position: relative;
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
  left: 50%;
  top: 50%;
  width: var(--hit-target);
  height: var(--hit-target);
  transform: translate(-50%, -50%);
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
