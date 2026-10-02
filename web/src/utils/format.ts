/** 字节数格式化 */
export function formatBytes(bytes?: number): string {
  const n = Number(bytes || 0)
  if (n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), units.length - 1)
  const v = n / Math.pow(1024, i)
  return `${v.toFixed(i === 0 ? 0 : 2)} ${units[i]}`
}

/** 秒数格式化为人性化时长 */
export function formatDuration(sec?: number): string {
  const s = Math.max(0, Math.floor(Number(sec || 0)))
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  const ss = s % 60
  if (d > 0) return `${d}天 ${h}小时`
  if (h > 0) return `${h}小时 ${m}分`
  if (m > 0) return `${m}分 ${ss}秒`
  return `${ss}秒`
}

/** 时间字符串本地化显示 */
export function formatTime(t?: string | number | null): string {
  if (!t) return '-'
  const d = new Date(t)
  if (Number.isNaN(d.getTime())) return '-'
  const p = (v: number) => String(v).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

/** 相对时间显示 */
export function formatRelative(t?: string | null): string {
  if (!t) return '-'
  const d = new Date(t).getTime()
  if (Number.isNaN(d)) return '-'
  const diff = Date.now() - d
  if (diff < 0) return formatTime(t)
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s} 秒前`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} 分钟前`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} 小时前`
  return formatTime(t)
}
