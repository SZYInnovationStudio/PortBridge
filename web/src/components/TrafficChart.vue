<template>
  <div class="chart">
    <div class="chart-head">
      <span class="chart-title">{{ title }}</span>
      <div class="legend">
        <span class="legend-item"><i class="sw in" />入站</span>
        <span class="legend-item"><i class="sw out" />出站</span>
      </div>
    </div>
    <svg v-if="points.length" class="svg" viewBox="0 0 800 240" preserveAspectRatio="none">
      <!-- 水平网格 -->
      <line v-for="(g, i) in gridY" :key="'g' + i" x1="0" :y1="g" x2="800" :y2="g" class="grid" />
      <!-- 入站面积 -->
      <polygon :points="areaIn" class="area-in" />
      <polyline :points="lineIn" class="line-in" />
      <!-- 出站面积 -->
      <polygon :points="areaOut" class="area-out" />
      <polyline :points="lineOut" class="line-out" />
    </svg>
    <div v-else class="empty-tip">暂无流量数据</div>
    <div v-if="points.length" class="x-axis">
      <span v-for="(l, i) in xLabels" :key="'x' + i">{{ l }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { formatTime } from '@/utils/format'
import type { TrafficPoint } from '@/api/types'

const props = withDefaults(defineProps<{ points: TrafficPoint[]; title?: string }>(), {
  title: '最近 24 小时流量',
})

const W = 800
const H = 240
const PAD = 8

const maxV = computed(() => {
  let m = 0
  for (const p of props.points) {
    m = Math.max(m, p.bytes_in || 0, p.bytes_out || 0)
  }
  return m > 0 ? m : 1
})

const gridY = computed(() => [0, 1, 2, 3, 4].map((i) => PAD + (i * (H - PAD * 2)) / 4))

function xy(idx: number, value: number): [number, number] {
  const n = Math.max(props.points.length - 1, 1)
  const x = (idx / n) * W
  const y = H - PAD - ((value || 0) / maxV.value) * (H - PAD * 2)
  return [x, y]
}

function buildLine(key: 'bytes_in' | 'bytes_out'): string {
  return props.points.map((p, i) => xy(i, p[key]).join(',')).join(' ')
}

function buildArea(key: 'bytes_in' | 'bytes_out'): string {
  const line = buildLine(key)
  return `0,${H} ${line} ${W},${H}`
}

const lineIn = computed(() => buildLine('bytes_in'))
const lineOut = computed(() => buildLine('bytes_out'))
const areaIn = computed(() => buildArea('bytes_in'))
const areaOut = computed(() => buildArea('bytes_out'))

const xLabels = computed(() => {
  const n = props.points.length
  if (!n) return []
  const step = Math.max(1, Math.floor(n / 6))
  const out: string[] = []
  for (let i = 0; i < n; i += step) {
    out.push(formatTime(props.points[i].time).slice(11, 16))
  }
  return out
})
</script>

<style scoped>
.chart {
  width: 100%;
}

.chart-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}

.chart-title {
  font-size: 14px;
  font-weight: 600;
}

.legend {
  display: flex;
  gap: 14px;
  font-size: 12px;
  color: #606266;
}

.legend-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

.sw {
  display: inline-block;
  width: 12px;
  height: 3px;
  border-radius: 2px;
}

.sw.in {
  background: #409eff;
}
.sw.out {
  background: #67c23a;
}

.svg {
  width: 100%;
  height: 240px;
  display: block;
}

.grid {
  stroke: #eef0f3;
  stroke-width: 1;
}

.line-in {
  fill: none;
  stroke: #409eff;
  stroke-width: 2;
  vector-effect: non-scaling-stroke;
}

.line-out {
  fill: none;
  stroke: #67c23a;
  stroke-width: 2;
  vector-effect: non-scaling-stroke;
}

.area-in {
  fill: rgba(64, 158, 255, 0.14);
  stroke: none;
}

.area-out {
  fill: rgba(103, 194, 58, 0.12);
  stroke: none;
}

.x-axis {
  display: flex;
  justify-content: space-between;
  color: #909399;
  font-size: 12px;
  margin-top: 6px;
}
</style>
