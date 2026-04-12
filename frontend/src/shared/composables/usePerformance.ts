import { ref, onMounted } from 'vue'

interface PerformanceMetrics {
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

  onMounted(() => {
    if (typeof window === 'undefined' || !('performance' in window)) {
      return
    }

    isSupported.value = true

    // Calculate basic metrics from navigation timing
    const calculateMetrics = () => {
      const navEntry = performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming
      
      if (!navEntry) return

      metrics.value = {
        dnsLookup: navEntry.domainLookupEnd - navEntry.domainLookupStart,
        tcpConnection: navEntry.connectEnd - navEntry.connectStart,
        serverResponse: navEntry.responseEnd - navEntry.responseStart,
        domProcessing: navEntry.domComplete - navEntry.domInteractive,
        resourceLoading: navEntry.loadEventEnd - navEntry.responseEnd,
        totalLoadTime: navEntry.loadEventEnd - navEntry.startTime,
      }
    }

    // Wait for load event
    if (document.readyState === 'complete') {
      calculateMetrics()
    } else {
      window.addEventListener('load', calculateMetrics)
    }

    // Observe Core Web Vitals if supported
    observeCoreWebVitals()
  })

  function observeCoreWebVitals() {
    // Largest Contentful Paint
    if ('PerformanceObserver' in window) {
      try {
        const lcpObserver = new PerformanceObserver((list) => {
          const entries = list.getEntries()
          if (entries.length > 0) {
            const lastEntry = entries[entries.length - 1]
            if (metrics.value && lastEntry) {
              metrics.value.lcp = lastEntry.startTime
            }
          }
        })
        lcpObserver.observe({ entryTypes: ['largest-contentful-paint'] })
      } catch {
        // Not supported
      }

      // First Input Delay
      try {
        const fidObserver = new PerformanceObserver((list) => {
          const firstEntry = list.getEntries()[0] as PerformanceEventTiming
          if (firstEntry && metrics.value) {
            metrics.value.fid = firstEntry.processingStart - firstEntry.startTime
          }
        })
        fidObserver.observe({ entryTypes: ['first-input'] })
      } catch {
        // Not supported
      }

      // Cumulative Layout Shift
      try {
        let clsValue = 0
        const clsObserver = new PerformanceObserver((list) => {
          for (const entry of list.getEntries()) {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            const layoutShift = entry as any
            if (!layoutShift.hadRecentInput) {
              clsValue += layoutShift.value
            }
          }
          if (metrics.value) {
            metrics.value.cls = clsValue
          }
        })
        clsObserver.observe({ entryTypes: ['layout-shift'] })
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
          if (fcpEntry && metrics.value) {
            metrics.value.fcp = fcpEntry.startTime
          }
        })
        paintObserver.observe({ entryTypes: ['paint'] })
      } catch {
        // Not supported
      }
    }
  }

  // Measure function execution time
  function measure<T>(name: string, fn: () => T): T {
    const start = performance.now()
    const result = fn()
    const end = performance.now()
    console.warn(`[Performance] ${name}: ${(end - start).toFixed(2)}ms`)
    return result
  }

  // Measure async function execution time
  async function measureAsync<T>(name: string, fn: () => Promise<T>): Promise<T> {
    const start = performance.now()
    const result = await fn()
    const end = performance.now()
    console.warn(`[Performance] ${name}: ${(end - start).toFixed(2)}ms`)
    return result
  }

  // Mark and measure
  function mark(name: string) {
    if (performance && performance.mark) {
      performance.mark(name)
    }
  }

  function measureBetween(startMark: string, endMark: string, measureName: string) {
    if (performance && performance.measure) {
      performance.measure(measureName, startMark, endMark)
      const entries = performance.getEntriesByName(measureName)
      for (const entry of entries) {
        if (entry) {
          return entry.duration
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
export function reportPerformanceMetrics(metrics: PerformanceMetrics) {
  // Send to analytics service
  if (typeof window !== 'undefined' && 'analytics' in window) {
    // @ts-expect-error - analytics might be defined globally
    window.analytics?.track('Performance Metrics', metrics)
  }

  // Log to console in development
  if (import.meta.env.DEV) {
    console.warn('[Performance Metrics]', metrics)
  }
}
