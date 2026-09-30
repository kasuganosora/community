/**
 * Tiny interruptible spring driver (no dependency).
 *
 * Parameterised the way Apple does it: damping ratio (1 = critically damped,
 * < 1 overshoots) and response (seconds to roughly reach the target; not a
 * duration). A running spring can be re-targeted at any time and keeps its
 * current position *and* velocity, so reversing mid-flight never jumps or
 * hits a velocity "brick wall".
 */
export interface SpringConfig {
  /** Damping ratio. 1.0 = no overshoot (default UI), ~0.8 after a flick. */
  damping: number
  /** Response in seconds. Lower = snappier. */
  response: number
}

export const SPRING_DEFAULT: SpringConfig = { damping: 1, response: 0.35 }
export const SPRING_SHEET_FLICK: SpringConfig = { damping: 0.8, response: 0.3 }
export const SPRING_EXIT: SpringConfig = { damping: 1, response: 0.26 }

export interface Spring {
  /** Current (presentation) value. */
  readonly value: number
  /** Current velocity in units/second. */
  readonly velocity: number
  readonly running: boolean
  /** Animate toward `target`, optionally overriding the velocity (px/s). */
  to(target: number, config?: SpringConfig, velocity?: number): Promise<boolean>
  /** Jump to a value with no animation (e.g. 1:1 while dragging). */
  set(value: number): void
  stop(): void
}

const REST_DELTA = 0.5
const REST_SPEED = 8

export function createSpring(initial: number, onUpdate: (v: number) => void): Spring {
  let value = initial
  let velocity = 0
  let target = initial
  let cfg: SpringConfig = SPRING_DEFAULT
  let raf = 0
  let last = 0
  let resolve: ((done: boolean) => void) | null = null

  const settle = (done: boolean) => {
    const r = resolve
    resolve = null
    r?.(done)
  }

  const step = (now: number) => {
    // Clamp long frames (tab switches) so the integration stays stable.
    let dt = Math.min((now - last) / 1000, 1 / 30)
    last = now
    const omega = (2 * Math.PI) / cfg.response
    const k = omega * omega
    const c = 2 * cfg.damping * omega
    // Semi-implicit Euler in fixed sub-steps: stable and cheap.
    const h = 1 / 240
    while (dt > 0) {
      const s = Math.min(h, dt)
      const a = -k * (value - target) - c * velocity
      velocity += a * s
      value += velocity * s
      dt -= s
    }
    if (Math.abs(value - target) < REST_DELTA && Math.abs(velocity) < REST_SPEED) {
      value = target
      velocity = 0
      raf = 0
      onUpdate(value)
      settle(true)
      return
    }
    onUpdate(value)
    raf = requestAnimationFrame(step)
  }

  return {
    get value() { return value },
    get velocity() { return velocity },
    get running() { return raf !== 0 },
    to(next, config = SPRING_DEFAULT, v) {
      target = next
      cfg = config
      if (v !== undefined) velocity = v
      settle(false) // a re-target supersedes the previous promise
      return new Promise<boolean>((res) => {
        resolve = res
        if (!raf) {
          last = performance.now()
          raf = requestAnimationFrame(step)
        }
      })
    },
    set(v) {
      if (raf) cancelAnimationFrame(raf)
      raf = 0
      value = v
      target = v
      velocity = 0
      onUpdate(value)
      settle(false)
    },
    stop() {
      if (raf) cancelAnimationFrame(raf)
      raf = 0
      settle(false)
    },
  }
}

/**
 * Where a flick is *going*: Apple's exponential-decay projection from the
 * Designing Fluid Interfaces sample code. decelerationRate 0.998 ≈ scroll
 * feel, 0.99 = snappier.
 */
export function project(velocityPxPerSec: number, decelerationRate = 0.99): number {
  return ((velocityPxPerSec / 1000) * decelerationRate) / (1 - decelerationRate)
}

/** Progressive resistance past a boundary (real things slow before they stop). */
export function rubberband(overshoot: number, dimension: number, constant = 0.55): number {
  return (overshoot * dimension * constant) / (dimension + constant * Math.abs(overshoot))
}

/**
 * After ~10px of vertical movement, decide whether the gesture moves the
 * sheet or the scrolling content. Pulling down at the top — or up when the
 * content is already at the bottom — takes the sheet (dismiss / rubber-band).
 * Any other direction belongs to the scroller, so the two gestures never fight.
 */
export function sheetDragIntent(
  dy: number,
  scrollTop: number,
  maxScroll: number,
): 'sheet' | 'content' {
  if (dy > 0 && scrollTop <= 0) return 'sheet'
  if (dy < 0 && scrollTop >= maxScroll - 0.5) return 'sheet'
  return 'content'
}

/**
 * Where a released sheet is going. Project the finger's velocity (Apple's
 * exponential decay, scroll-like 0.998), then pick the snap — open at 0 or
 * closed at `height` — nearest that projected point. A flick aimed up
 * returns even if the finger let go past the midpoint; a flick aimed down
 * dismisses even if it hadn't crossed halfway. A dead tie follows the
 * velocity's sign, and a resting finger stays open.
 */
export function shouldDismissSheet(position: number, velocity: number, height: number): boolean {
  if (height <= 0) return velocity > 0
  const projected = position + project(velocity, 0.998)
  const openDist = Math.abs(projected)
  const closedDist = Math.abs(projected - height)
  if (Math.abs(openDist - closedDist) < 0.5) return velocity > 0
  return closedDist < openDist
}

/** Velocity (px/s) from a short pointer history of [timeMs, position]. */
export function velocityFrom(history: Array<[number, number]>, windowMs = 100): number {
  if (history.length < 2) return 0
  const lastSample = history[history.length - 1]!
  let first = history[0]!
  for (let i = history.length - 1; i >= 0; i--) {
    const sample = history[i]!
    if (lastSample[0] - sample[0] > windowMs) break
    first = sample
  }
  const dt = lastSample[0] - first[0]
  return dt > 0 ? ((lastSample[1] - first[1]) / dt) * 1000 : 0
}

export function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined'
    && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
}
