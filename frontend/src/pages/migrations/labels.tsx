import type { MigrationMode, MigrationState } from '../../api/types'
import Badge, { type BadgeTone } from '../../components/Badge'

// warna dikelompokkan menurut tindakan yang dibutuhkan: merah berarti harus dipulihkan dulu
const stateStyle: Record<MigrationState, { label: string; tone: BadgeTone }> = {
  draft: { label: 'Draft', tone: 'outline' },
  ready: { label: 'Siap diajukan', tone: 'butter' },
  applied: { label: 'Aktif', tone: 'green' },
  failed: { label: 'Gagal', tone: 'red' },
  rolled_back: { label: 'Sudah di-rollback', tone: 'amber' },
  partial: { label: 'Setengah jalan', tone: 'red' },
  partially_reverted: { label: 'Rollback setengah', tone: 'red' },
  unknown: { label: 'Hasil belum pasti', tone: 'red' },
  cancelled: { label: 'Dibatalkan', tone: 'neutral' },
  superseded: { label: 'Digantikan', tone: 'neutral' },
}

export const modeLabel: Record<MigrationMode, string> = { normal: 'Normal', batch: 'Batch' }

export function StateBadge({ state }: { state: MigrationState }) {
  const style = stateStyle[state]
  return (
    <Badge tone={style.tone} dot>
      {style.label}
    </Badge>
  )
}
