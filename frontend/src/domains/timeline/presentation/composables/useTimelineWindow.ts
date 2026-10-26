import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch, type Ref } from 'vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { sessionEpoch } from '@/shared/api/sessionScope'

const ESTIMATED_HEIGHT = 240
const OVERSCAN = 1200
export const MAX_TIMELINE_CARDS = 60
export const MAX_TIMELINE_PINS = 8
export const MAX_TIMELINE_MEASUREMENTS = 512
const snapshots = new Map<string, { first: string; prefix: number; heights: [string, number][]; flags: [string, Record<string, boolean>][] }>()
watch(sessionEpoch, () => snapshots.clear(), { flush: 'sync' })

/** Window scrolling, deliberately specific to timeline cards. No retained status objects. */
export function useTimelineWindow(
  statuses: () => MastodonStatus[],
  scope: () => string,
  hasPrevious: () => boolean,
  region: Ref<HTMLElement | undefined>,
  onPins: (ids: string[]) => void,
) {
  const heights = new Map<string, number>()
  const flags = new Map<string, Record<string, boolean>>()
  const elements = new Map<string, HTMLElement>()
  const measuredGeneration = new WeakMap<HTMLElement, number>()
  const pins = shallowRef<string[]>([])
  const revision = ref(0)
  const renderVersion = ref(0)
  const prefix = ref(0)
  const scrollTop = ref(0)
  const viewportHeight = ref(800)
  let observer: ResizeObserver | undefined
  let frame = 0
  let disposed = false
  let restoring = false
  let generation = 0
  let navigation = 0
  const key = () => `${sessionEpoch.value}:${scope()}`
  let activeKey = key()
  const ids = computed(() => statuses().map(status => status.id))
  let currentFirst = ids.value[0]
  let scopeIds: string[] | undefined
  const offsets = computed(() => {
    void revision.value
    const result = [0]
    for (const id of ids.value) result.push(result[result.length - 1]! + (heights.get(id) ?? ESTIMATED_HEIGHT))
    return result
  })
  const totalHeight = computed(() => prefix.value + offsets.value[offsets.value.length - 1]!)
  function indexAt(position: number, values = offsets.value) {
    let low = 0
    let high = Math.max(0, values.length - 2)
    while (low < high) {
      const mid = Math.ceil((low + high) / 2)
      if (values[mid]! <= position) low = mid
      else high = mid - 1
    }
    return low
  }
  const visible = computed(() => {
    if (!statuses().length) return []
    const local = scrollTop.value - prefix.value
    const first = indexAt(Math.max(0, local - OVERSCAN))
    const last = Math.min(statuses().length, indexAt(local + viewportHeight.value + OVERSCAN) + 1, first + MAX_TIMELINE_CARDS)
    const selected = new Set<number>()
    for (let index = first; index < last; index++) selected.add(index)
    for (const id of pins.value) {
      const index = ids.value.indexOf(id)
      if (index >= 0) selected.add(index)
    }
    let end = 0
    return [...selected].sort((a, b) => a - b).map(index => {
      const top = prefix.value + offsets.value[index]!
      const row = { status: statuses()[index]!, index, gap: Math.max(0, top - end) }
      end = prefix.value + offsets.value[index + 1]!
      return row
    })
  })
  const tail = computed(() => {
    const last = visible.value[visible.value.length - 1]
    return last ? totalHeight.value - prefix.value - offsets.value[last.index + 1]! : 0
  })
  function origin() { return (region.value?.getBoundingClientRect().top ?? 0) + window.scrollY }
  function readScroll() {
    scrollTop.value = Math.max(0, window.scrollY - origin())
    viewportHeight.value = window.innerHeight
  }
  function schedule() {
    if (!frame) frame = requestAnimationFrame(() => { frame = 0; readScroll() })
  }
  function anchor(oldIds = ids.value, oldOffsets = offsets.value) {
    // Keep the signed distance before the feed starts. The clamped scrollTop
    // used for virtualization would otherwise anchor row one at the viewport
    // top and jump past the page header/composer on its first measurement.
    const viewportTop = window.scrollY - origin()
    const index = indexAt(Math.max(0, viewportTop - prefix.value), oldOffsets)
    return { id: oldIds[index], top: prefix.value + (oldOffsets[index] ?? 0) - viewportTop }
  }
  async function restore(point: ReturnType<typeof anchor>) {
    const expected = generation
    const expectedNavigation = navigation
    await nextTick()
    if (disposed || expected !== generation || expectedNavigation !== navigation || !point.id) return
    const index = ids.value.indexOf(point.id)
    if (index < 0) return
    const desired = origin() + prefix.value + offsets.value[index]! - point.top
    if (Math.abs(window.scrollY - desired) > 0.5) window.scrollTo({ top: Math.max(0, desired), behavior: 'instant' })
    readScroll()
  }
  async function revealHead(headerHeight: number, focus: boolean) {
    const expected = generation
    navigation++
    // A deliberate jump to a freshly committed head supersedes compensation
    // for the old reading anchor, including the evicted-page spacer.
    prefix.value = 0
    scrollTop.value = 0
    await nextTick()
    if (disposed || expected !== generation) return
    window.scrollTo({ top: Math.max(0, origin() - headerHeight), behavior: 'instant' })
    readScroll()
    if (focus) region.value?.focus({ preventScroll: true })
  }
  function trimMetadata() {
    // Current eight-page content normally fits entirely. For external callers with
    // much larger arrays retain only recently measured/stateful rows, never 5000 maps.
    for (const map of [heights, flags]) {
      for (const id of map.keys()) {
        if (map.size <= MAX_TIMELINE_MEASUREMENTS) break
        if (!elements.has(id) && !pins.value.includes(id)) map.delete(id)
      }
    }
  }
  function measure(entries: ResizeObserverEntry[]) {
    const point = anchor()
    let changed = false
    for (const entry of entries) {
      const id = (entry.target as HTMLElement).dataset.timelineId
      const height = entry.borderBoxSize?.[0]?.blockSize ?? entry.target.getBoundingClientRect().height
      if (!id || elements.get(id) !== entry.target || measuredGeneration.get(entry.target as HTMLElement) !== generation || !Number.isFinite(height) || height < 0 || heights.get(id) === height) continue
      heights.delete(id)
      heights.set(id, height)
      changed = true
    }
    if (changed) {
      trimMetadata()
      revision.value++
      void restore(point)
    }
  }
  function bind(id: string, element: unknown) {
    const node = element && '$el' in Object(element) ? (element as { $el: HTMLElement }).$el : element
    const old = elements.get(id)
    if (old === node) return
    if (old) observer?.unobserve(old)
    if (node instanceof HTMLElement) {
      elements.set(id, node)
      measuredGeneration.set(node, generation)
      observer?.observe(node)
    } else elements.delete(id)
  }
  function rowFlags(id: string) {
    let value = flags.get(id)
    if (!value) { value = reactive({}); flags.set(id, value); trimMetadata() }
    return value
  }
  function pin(id: string, active: boolean) {
    if (active && pins.value.includes(id)) return true
    if (active && pins.value.length >= MAX_TIMELINE_PINS) return false
    const next = pins.value.filter(value => value !== id)
    if (active && ids.value.includes(id)) next.push(id)
    if (next.length !== pins.value.length) {
      pins.value = next
      onPins(next)
    }
    return true
  }
  function save() {
    if (!currentFirst) return
    snapshots.delete(activeKey)
    snapshots.set(activeKey, { first: currentFirst, prefix: prefix.value, heights: [...heights], flags: [...flags].map(([id, value]) => [id, { ...value }]) })
    while (snapshots.size > 8) snapshots.delete(snapshots.keys().next().value!)
  }
  function reset() {
    generation++
    renderVersion.value++
    heights.clear(); flags.clear(); pins.value = []; prefix.value = 0
    onPins([])
    revision.value++
  }
  function loadSnapshot() {
    const saved = snapshots.get(activeKey)
    if (!saved || saved.first !== ids.value[0]) return
    prefix.value = saved.prefix
    for (const [id, height] of saved.heights) heights.set(id, height)
    for (const [id, value] of saved.flags) flags.set(id, reactive({ ...value }))
    revision.value++
  }
  loadSnapshot()
  watch([scope, sessionEpoch], ([, epoch], [, previousEpoch]) => {
    // An auth transition never saves the old account's geometry/interaction state.
    if (epoch === previousEpoch) save()
    reset(); activeKey = key(); currentFirst = ids.value[0]
    scopeIds = ids.value
    loadSnapshot(); readScroll()
  }, { flush: 'sync' })
  watch(ids, (next, previous) => {
    currentFirst = next[0]
    if (scopeIds === next) { scopeIds = undefined; return }
    if (!previous.length) { loadSnapshot(); return }
    const oldOffsets = [0]
    for (const id of previous) oldOffsets.push(oldOffsets[oldOffsets.length - 1]! + (heights.get(id) ?? ESTIMATED_HEIGHT))
    const point = anchor(previous, oldOffsets)
    const dropped = next.length ? previous.indexOf(next[0]!) : -1
    const prepended = next.indexOf(previous[0]!)
    if (dropped > 0 && hasPrevious()) prefix.value += oldOffsets[dropped]!
    else if (prepended > 0) {
      prefix.value = Math.max(0, prefix.value - next.slice(0, prepended).reduce((sum, id) => sum + (heights.get(id) ?? ESTIMATED_HEIGHT), 0))
    } else if (!next.some(id => previous.includes(id))) reset()
    const allowed = new Set(next)
    for (const id of heights.keys()) if (!allowed.has(id)) heights.delete(id)
    for (const id of flags.keys()) if (!allowed.has(id)) flags.delete(id)
    pins.value = pins.value.filter(id => allowed.has(id))
    onPins(pins.value)
    revision.value++
    if (!restoring) void restore(point)
  }, { flush: 'pre' })
  onMounted(() => {
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(measure)
      for (const element of elements.values()) observer.observe(element)
    }
    readScroll()
    window.addEventListener('scroll', schedule, { passive: true })
    window.addEventListener('resize', schedule, { passive: true })
    // Router applies savedPosition on its next animation frame.
    restoring = true
    frame = requestAnimationFrame(() => { frame = 0; restoring = false; readScroll() })
  })
  onBeforeUnmount(() => {
    save(); disposed = true
    observer?.disconnect()
    window.removeEventListener('scroll', schedule)
    window.removeEventListener('resize', schedule)
    if (frame) cancelAnimationFrame(frame)
    onPins([])
  })
  return { visible, tail, prefix, bind, rowFlags, pin, scrollTop, totalHeight, renderVersion, revealHead }
}
