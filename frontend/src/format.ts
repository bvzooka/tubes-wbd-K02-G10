// Format tampilan yang dipakai banyak halaman.

const dateTime = new Intl.DateTimeFormat('id-ID', {
  day: '2-digit',
  month: 'short',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
})

// formatDateTime menulis waktu ISO dari API sebagai "23 Nov 2026 13.54" (zona waktu browser).
export function formatDateTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : dateTime.format(d)
}

export function formatNumber(n: number, digits = 2): string {
  return n.toLocaleString('id-ID', { maximumFractionDigits: digits })
}
