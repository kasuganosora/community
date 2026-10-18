<script setup lang="ts">
import { computed } from 'vue'
// Dependency-free SVG sparkline; series share one y scale.
const props = withDefaults(defineProps<{ series: { values: number[]; tone: 'accent' | 'danger' | 'warning' | 'muted' }[]; height?: number; label: string }>(), { height: 40 })
const W = 120
const paths = computed(() => {
  const max = Math.max(1, ...props.series.flatMap(s => s.values))
  return props.series.map(s => {
    const n = s.values.length
    if (n === 0) return { tone: s.tone, d: '', point: null }
    const step = n > 1 ? W / (n - 1) : 0
    const pts = s.values.map((v, i) => `${(n > 1 ? i * step : W / 2).toFixed(1)},${(props.height - 2 - (v / max) * (props.height - 4)).toFixed(1)}`)
    return { tone: s.tone, d: n > 1 ? `M${pts.join('L')}` : '', point: n === 1 ? pts[0]!.split(',') : null }
  })
})
const empty = computed(() => props.series.every(s => s.values.length === 0))
</script>

<template>
  <div class="queue-spark-wrap">
  <svg class="queue-spark" :viewBox="`0 0 ${W} ${height}`" preserveAspectRatio="none" role="img" :aria-label="label" :style="{ height: `${height}px` }">
    <line x1="0" :y1="height - 1" :x2="W" :y2="height - 1" class="queue-spark__base" />
    <template v-for="(p, i) in paths" :key="i">
      <path v-if="p.d" :d="p.d" :class="`queue-spark__line queue-spark__line--${p.tone}`" vector-effect="non-scaling-stroke" />
      <circle v-else-if="p.point" :cx="p.point[0]" :cy="p.point[1]" r="1" :class="`queue-spark__line queue-spark__line--${p.tone}`" vector-effect="non-scaling-stroke" />
    </template>
  </svg>
  <span v-if="empty" class="queue-spark__empty">{{ $t('admin.QueueSparkline.s1') }}</span>
  </div>
</template>

<style scoped>
.queue-spark { display: block; width: 100%; overflow: visible; }
.queue-spark__base { stroke: var(--color-separator); stroke-width: 1; vector-effect: non-scaling-stroke; }
.queue-spark__line { fill: none; stroke-width: 1.5; stroke-linejoin: round; stroke-linecap: round; }
.queue-spark__line--accent { stroke: var(--admin-accent, var(--color-primary)); }
.queue-spark__line--danger { stroke: var(--color-danger); }
.queue-spark__line--warning { stroke: var(--color-warning, #ff9500); }
.queue-spark__line--muted { stroke: var(--color-label-tertiary); }
.queue-spark-wrap { position: relative; }
.queue-spark__empty { position: absolute; inset: 0; display: grid; place-items: center; font-size: 11px; color: var(--color-label-tertiary); }
</style>
