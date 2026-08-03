import { ref, onMounted, onUnmounted } from 'vue'

/** Minimal interface for LayoutShift performance entries.
 *  Not yet in TypeScript's built-in DOM lib but available in all modern browsers. */
interface LayoutShift extends PerformanceEntry {
  readonly value: number
  readonly hadRecentInput: boolean
}

/** Minimal interface for PerformanceEventTiming (First Input Delay).
 *  Not yet in TypeScript's built-in DOM lib but available in all modern browsers. */
interface PerformanceEventTiming extends PerformanceEntry {
  readonly processingStart: number
  readonly processingEnd: number
  readonly cancelable: boolean
}

/** Analytics service shape on the global window object */
interface AnalyticsService {
  track: (event: string, data: unknown) => void
}

declare global {
  interface Window {
    analytics?: AnalyticsService
  }
}

export interface PerformanceMetrics {
  // Navigation timing
  dnsLookup: number
  tcpConnection: number
  serverResponse: number
  domProcessing: number
  resourceLoading: number
  totalLoadTime: number

  // Core Web Vitals (if available)
  lcp?: number // Largest Contentful Paint
  fid?: number // First Input Delay
  cls?: number // Cumulative Layout Shift
  fcp?: number // First Contentful Paint
  ttfb?: number // Time to First Byte
}

export function usePerformance() {
  const metrics = ref<PerformanceMetrics | null>(null)
  const isSupported = ref(false)

  // ── Store web vitals at composable scope so both navigation-metrics and
  //    observer callbacks can access the same store. ──
  const webVitals: Partial<Pick<PerformanceMetrics, 'lcp' | 'fid' | 'cls' | 'fcp' | 'ttfb'>> = {}
  const observers: PerformanceObserver[] = []

  // ── Navigation timing calculation (pure, no side-effects on registration) ──
  function calculateNavigationMetrics(): void {
    const navEntries = performance.getEntriesByType('navigation')
    const navEntry = navEntries[0] as PerformanceNavigationTiming | undefined
    if (!navEntry) return

    const loadEnd = navEntry.loadEventEnd || performance.now()

    metrics.value = {
      dnsLookup: navEntry.domainLookupEnd - navEntry.domainLookupStart,
      tcpConnection: navEntry.connectEnd - navEntry.connectStart,
      serverResponse: navEntry.responseEnd - navEntry.responseStart,
      domProcessing: navEntry.domComplete - navEntry.domInteractive,
      resourceLoading: loadEnd - navEntry.responseEnd,
      totalLoadTime: loadEnd - navEntry.startTime,
      ttfb: navEntry.responseStart - navEntry.requestStart,
    }
  }

  // ── Merge any web-vitals that arrived before metrics.value was set ──
  function syncWebVitals(): void {
    if (!metrics.value) return
    if (webVitals.lcp !== undefined) metrics.value.lcp = webVitals.lcp
    if (webVitals.fid !== undefined) metrics.value.fid = webVitals.fid
    if (webVitals.cls !== undefined) metrics.value.cls = webVitals.cls
    if (webVitals.fcp !== undefined) metrics.value.fcp = webVitals.fcp
    if (webVitals.ttfb !== undefined) metrics.value.ttfb = webVitals.ttfb
  }

  // ── Single stable load-handler reference — registered and removed with the
  //    SAME function object, guaranteeing correct cleanup. ──
  function onLoadHandler(): void {
    calculateNavigationMetrics()
    syncWebVitals()
  }

  onMounted(() => {
    if (typeof window === 'undefined' || !('performance' in window)) {
      return
    }

    isSupported.value = true

    if (document.readyState === 'complete') {
      onLoadHandler()
    } else {
      window.addEventListener('load', onLoadHandler)
    }

    observeCoreWebVitals()
  })

  onUnmounted(() => {
    // Clean up event listener — uses the SAME reference registered above
    window.removeEventListener('load', onLoadHandler)

    // Disconnect all PerformanceObservers
    for (const observer of observers) {
      observer.disconnect()
    }
    observers.length = 0
  })

  function observeCoreWebVitals(): void {
    if (!('PerformanceObserver' in window)) return

    // Largest Contentful Paint
    try {
      const lcpObserver = new PerformanceObserver((list) => {
        const entries = list.getEntries()
        if (entries.length > 0) {
          const lastEntry = entries[entries.length - 1]
          if (lastEntry) {
            webVitals.lcp = lastEntry.startTime
            if (metrics.value) metrics.value.lcp = lastEntry.startTime
          }
        }
      })
      lcpObserver.observe({ entryTypes: ['largest-contentful-paint'] })
      observers.push(lcpObserver)
    } catch {
      // Not supported
    }

    // First Input Delay
    try {
      const fidObserver = new PerformanceObserver((list) => {
        const firstEntry = list.getEntries()[0] as PerformanceEventTiming | undefined
        if (firstEntry) {
          const fid = firstEntry.processingStart - firstEntry.startTime
          webVitals.fid = fid
          if (metrics.value) metrics.value.fid = fid
        }
      })
      fidObserver.observe({ entryTypes: ['first-input'] })
      observers.push(fidObserver)
    } catch {
      // Not supported
    }

    // Cumulative Layout Shift
    try {
      let clsValue = 0
      const clsObserver = new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) {
          const layoutShift = entry as LayoutShift
          if (!layoutShift.hadRecentInput) {
            clsValue += layoutShift.value
          }
        }
        webVitals.cls = clsValue
        if (metrics.value) metrics.value.cls = clsValue
      })
      clsObserver.observe({ entryTypes: ['layout-shift'] })
      observers.push(clsObserver)
    } catch {
      // Not supported
    }

    // First Contentful Paint
    try {
      const paintObserver = new PerformanceObserver((list) => {
        const entries = list.getEntries()
        const fcpEntry = entries.find(
          (entry) => entry.name === 'first-contentful-paint'
        )
        if (fcpEntry) {
          webVitals.fcp = fcpEntry.startTime
          if (metrics.value) metrics.value.fcp = fcpEntry.startTime
        }
      })
      paintObserver.observe({ entryTypes: ['paint'] })
      observers.push(paintObserver)
    } catch {
      // Not supported
    }
  }

  // Measure function execution time
  function measure<T>(name: string, fn: () => T): T {
    if (typeof performance === 'undefined') return fn()
    const start = performance.now()
    try {
      const result = fn()
      const end = performance.now()
      if (import.meta.env.DEV) console.warn(`[Performance] ${name}: ${(end - start).toFixed(2)}ms`)
      return result
    } catch (err) {
      const end = performance.now()
      if (import.meta.env.DEV) console.warn(`[Performance] ${name}: ${(end - start).toFixed(2)}ms (FAILED)`)
      throw err
    }
  }

  // Measure async function execution time
  async function measureAsync<T>(name: string, fn: () => Promise<T>): Promise<T> {
    if (typeof performance === 'undefined') return fn()
    const start = performance.now()
    try {
      const result = await fn()
      const end = performance.now()
      if (import.meta.env.DEV) console.warn(`[Performance] ${name}: ${(end - start).toFixed(2)}ms`)
      return result
    } catch (err) {
      const end = performance.now()
      if (import.meta.env.DEV) console.warn(`[Performance] ${name}: ${(end - start).toFixed(2)}ms (FAILED)`)
      throw err
    }
  }

  // Mark and measure
  function mark(name: string): void {
    if (typeof performance !== 'undefined' && performance.mark) {
      performance.mark(name)
    }
  }

  function measureBetween(startMark: string, endMark: string, measureName: string): number | null {
    if (typeof performance !== 'undefined' && performance.measure) {
      performance.measure(measureName, startMark, endMark)
      const entries = performance.getEntriesByName(measureName)
      for (const entry of entries) {
        if (entry) {
          const duration = entry.duration
          performance.clearMeasures(measureName)
          return duration
        }
      }
    }
    return null
  }

  return {
    metrics,
    isSupported,
    measure,
    measureAsync,
    mark,
    measureBetween,
  }
}

// Report performance metrics to analytics
export function reportPerformanceMetrics(metrics: PerformanceMetrics): void {
  // Send to analytics service
  if (typeof window !== 'undefined' && window.analytics) {
    window.analytics.track('Performance Metrics', metrics)
  }

  // Log to console in development
  if (import.meta.env.DEV) {
    console.warn('[Performance Metrics]', metrics)
  }
}
