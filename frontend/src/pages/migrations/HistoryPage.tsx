import { Link, useParams, useSearchParams } from 'react-router-dom'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { HistoryEntry, HistoryList, HistoryStatus } from '../../api/types'
import Breadcrumbs from '../../components/Breadcrumbs'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Loading from '../../components/Loading'
import Pager from '../../components/Pager'
import PageHeader from '../../components/PageHeader'
import { useDatabaseName } from '../../hooks/useDatabaseName'
import { databasePath } from '../databases/paths'
import { formatTime, kindLabel, StatusBadge } from './labels'
import { executionPath, migrationPath, migrationsPath } from './paths'

const statusOptions: { value: HistoryStatus; label: string }[] = [
  { value: 'success', label: 'Berhasil' },
  { value: 'failed', label: 'Gagal' },
  { value: 'partial', label: 'Setengah jalan' },
  { value: 'outcome_unknown', label: 'Hasil belum pasti' },
]

export default function HistoryPage() {
  const { id = '' } = useParams()
  // saringan disimpan di URL supaya tetap ada setelah halaman dimuat ulang atau dibagikan
  const [params, setParams] = useSearchParams()
  const hideRollback = params.get('hide_rollback') === '1'
  const status = params.get('status') ?? ''
  const page = Math.max(Number(params.get('page')) || 1, 1)

  const query = new URLSearchParams({ page: String(page) })
  if (hideRollback) query.set('hide_rollback', '1')
  if (status) query.set('status', status)

  const history = useQuery({
    queryKey: ['migrations', id, 'history', hideRollback, status, page],
    queryFn: () => api<HistoryList>('GET', `/databases/${id}/executions?${query}`),
    // baris lama tetap tampil selagi yang baru diambil, jadi tabel tidak berkedip
    placeholderData: keepPreviousData,
  })
  const dbName = useDatabaseName(id)

  // mengganti saringan selalu kembali ke halaman pertama
  function setFilter(name: string, value: string) {
    const next = new URLSearchParams(params)
    if (value) next.set(name, value)
    else next.delete(name)
    if (name !== 'page') next.delete('page')
    setParams(next)
  }

  return (
    <>
      <Breadcrumbs
        items={[
          { label: 'Database', to: '/databases' },
          { label: dbName, to: databasePath(id) },
          { label: 'Migration', to: migrationsPath(id) },
          { label: 'Riwayat' },
        ]}
      />
      <PageHeader
        eyebrow={{ icon: 'history', label: dbName }}
        title="Riwayat eksekusi"
        subtitle="Setiap percobaan tercatat, berhasil maupun gagal, dan tidak bisa diubah."
      />

      <div className="mb-4 flex flex-wrap items-center gap-4">
        <label className="check">
          <input
            type="checkbox"
            checked={hideRollback}
            onChange={(e) => setFilter('hide_rollback', e.target.checked ? '1' : '')}
          />
          Hide rollback
        </label>
        <select
          className="input w-auto max-w-full"
          aria-label="Saring status"
          value={status}
          onChange={(e) => setFilter('status', e.target.value)}
        >
          <option value="">Semua status</option>
          {statusOptions.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </div>

      {history.isLoading && <Loading />}
      <ErrorMessage error={history.error} />
      {history.data?.items.length === 0 && <EmptyState icon="history" message="Tidak ada riwayat yang cocok." />}

      {history.data && history.data.items.length > 0 && (
        <>
          <div className="space-y-3">
            {history.data.items.map((h) => (
              <HistoryCard key={h.id} entry={h} databaseID={id} />
            ))}
          </div>
          <Pager
            page={page}
            hasNext={history.data.has_next}
            disabled={history.isFetching}
            onChange={(p) => setFilter('page', String(p))}
          />
        </>
      )}
    </>
  )
}

// satu kartu per percobaan, dipakai kartu daripada tabel karena isinya banyak dan harus muat di ponsel
function HistoryCard({ entry: h, databaseID }: { entry: HistoryEntry; databaseID: string }) {
  return (
    <article className="card space-y-2 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="m-0 text-[1.05rem]">
          {kindLabel[h.kind]}{' '}
          <Link to={migrationPath(databaseID, h.version)} className="tap-link">
            v{h.version} revisi {h.revision}
          </Link>
        </h2>
        <StatusBadge status={h.status} />
      </div>
      <p className="m-0 text-[0.86rem] text-muted">
        Eksekusi{' '}
        <Link to={executionPath(h.execution_id)} className="tap-link">
          #{h.execution_id}
        </Link>{' '}
        · percobaan {h.attempt} · migration ID {h.migration_id} · oleh {h.executed_by} · {formatTime(h.finished_at)}
      </p>
      {h.rolls_back_execution_id && (
        <p className="m-0 text-[0.86rem]">
          Membatalkan eksekusi{' '}
          <Link to={executionPath(h.rolls_back_execution_id)} className="tap-link">
            #{h.rolls_back_execution_id}
          </Link>
        </p>
      )}
      {h.batches_committed > 0 && (
        <p className="m-0 text-[0.86rem] text-muted">
          {h.batches_committed} batch, {h.rows_committed} baris tercommit
        </p>
      )}
      {h.failure_message && <p className="m-0 break-words text-[0.86rem] text-danger">{h.failure_message}</p>}
      <details>
        <summary className="cursor-pointer text-[0.86rem] font-bold text-green max-md:py-3">Lihat SQL</summary>
        <pre className="mb-0 mt-2 max-h-72 overflow-auto rounded-md bg-ink p-3 font-mono text-xs text-paper-soft">
          {h.sql || '(tidak ada SQL yang dijalankan)'}
        </pre>
      </details>
    </article>
  )
}
