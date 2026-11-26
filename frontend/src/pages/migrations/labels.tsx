import type { ApprovalStatus, ExecutionKind, ExecutionStatus, MigrationMode, MigrationState } from '../../api/types'
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
export const kindLabel: Record<ExecutionKind, string> = { apply: 'Apply', rollback: 'Rollback', forward: 'Forward' }

const statusStyle: Record<ExecutionStatus, { label: string; tone: BadgeTone }> = {
  pending_review: { label: 'Menunggu persetujuan', tone: 'butter' },
  approved: { label: 'Disetujui', tone: 'lime' },
  rejected: { label: 'Ditolak', tone: 'red' },
  cancelled: { label: 'Dibatalkan', tone: 'neutral' },
  queued: { label: 'Dalam antrean', tone: 'butter' },
  running: { label: 'Berjalan', tone: 'ink' },
  success: { label: 'Berhasil', tone: 'green' },
  failed: { label: 'Gagal', tone: 'red' },
  partial: { label: 'Setengah jalan', tone: 'red' },
  partially_reverted: { label: 'Rollback setengah', tone: 'red' },
  outcome_unknown: { label: 'Hasil belum pasti', tone: 'red' },
}

const approvalStyle: Record<ApprovalStatus, { label: string; tone: BadgeTone }> = {
  pending: { label: 'Menunggu', tone: 'butter' },
  approved: { label: 'Setuju', tone: 'green' },
  rejected: { label: 'Menolak', tone: 'red' },
}

// status yang masih bisa berubah sendiri atau masih menunggu tindakan
export function isOpen(status: ExecutionStatus) {
  return status === 'pending_review' || status === 'approved' || status === 'queued' || status === 'running'
}

export function StatusBadge({ status }: { status: ExecutionStatus }) {
  const style = statusStyle[status]
  return (
    <Badge tone={style.tone} dot>
      {style.label}
    </Badge>
  )
}

export function ApprovalBadge({ status }: { status: ApprovalStatus }) {
  const style = approvalStyle[status]
  return <Badge tone={style.tone}>{style.label}</Badge>
}

export function formatTime(value: string | null) {
  return value ? new Date(value).toLocaleString('id-ID') : '–'
}

export function StateBadge({ state }: { state: MigrationState }) {
  const style = stateStyle[state]
  return (
    <Badge tone={style.tone} dot>
      {style.label}
    </Badge>
  )
}
