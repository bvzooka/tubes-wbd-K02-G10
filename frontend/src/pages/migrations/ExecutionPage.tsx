import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { Approval, ExecutionDetail } from '../../api/types'
import Breadcrumbs from '../../components/Breadcrumbs'
import ErrorMessage from '../../components/ErrorMessage'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import StatCard from '../../components/StatCard'
import { useDatabaseName } from '../../hooks/useDatabaseName'
import { useSession } from '../../hooks/useSession'
import { databasePath } from '../databases/paths'
import { ApprovalBadge, formatTime, kindLabel, modeLabel, StatusBadge } from './labels'
import SqlBlock from './SqlBlock'
import { migrationPath, migrationsPath } from './paths'

const pollMs = 2000

export default function ExecutionPage() {
  const { executionID = '' } = useParams()
  const queryClient = useQueryClient()
  const session = useSession()
  const key = ['executions', executionID]

  const detail = useQuery({
    queryKey: key,
    queryFn: () => api<ExecutionDetail>('GET', `/executions/${executionID}`),
    // bertanya tiap 2 detik hanya selama worker masih bisa mengubah hasilnya, lalu berhenti
    refetchInterval: (query) => {
      const status = query.state.data?.execution.status
      return status === 'queued' || status === 'running' ? pollMs : false
    },
  })

  const dbName = useDatabaseName(detail.data?.execution.connection_id)

  // status migration dan kotak persetujuan ikut berubah, jadi cache-nya dibuang juga
  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: key })
    queryClient.invalidateQueries({ queryKey: ['migrations'] })
    queryClient.invalidateQueries({ queryKey: ['approvals'] })
  }
  const run = useMutation({
    mutationFn: () => api<{ job_id: string }>('POST', `/executions/${executionID}/run`),
    onSettled: refresh,
  })
  const cancel = useMutation({
    mutationFn: () => api('POST', `/executions/${executionID}/cancel`),
    onSettled: refresh,
  })
  const decide = useMutation({
    mutationFn: (d: { groupID: number; approve: boolean; comment: string }) =>
      api('POST', `/executions/${executionID}/approvals/${d.groupID}/${d.approve ? 'approve' : 'reject'}`, {
        comment: d.comment,
      }),
    onSettled: refresh,
  })

  if (detail.isLoading) return <Loading />
  if (!detail.data) return <ErrorMessage error={detail.error} />
  const { execution: e, approvals, attempts, job_id, can_edit } = detail.data
  const waiting = e.status === 'pending_review' || e.status === 'approved'
  const working = e.status === 'queued' || e.status === 'running'

  // tombol setuju hanya muncul untuk leader group yang keputusannya masih ditunggu
  const canDecide = (a: Approval) =>
    e.status === 'pending_review' && a.status === 'pending' && a.leader_id === session.data?.user.id

  return (
    <>
      <Breadcrumbs
        items={[
          { label: 'Database', to: '/databases' },
          { label: dbName, to: databasePath(e.connection_id) },
          { label: 'Migration', to: migrationsPath(e.connection_id) },
          { label: `v${e.version}`, to: migrationPath(e.connection_id, e.version) },
          { label: `Permintaan #${e.id}` },
        ]}
      />
      <PageHeader
        eyebrow={{ icon: 'bolt', label: `${modeLabel[e.mode]} · revisi ${e.revision}` }}
        title={`${e.resumes_execution_id ? 'Resume' : kindLabel[e.kind]} v${e.version}`}
        titleExtra={<StatusBadge status={e.status} />}
        subtitle={`Diajukan ${e.requested_by}, ${formatTime(e.requested_at)}`}
        aside={
          e.mode === 'batch' && (
            <>
              <StatCard icon="layers" label="Batch tercommit" value={e.batches_committed} />
              <StatCard icon="grid" label="Baris tercommit" value={e.rows_committed} />
            </>
          )
        }
      />

      <div className="mb-5 space-y-3">
        <ErrorMessage error={run.error ?? cancel.error ?? decide.error} />
        {working && (
          <p role="status" className="alert">
            <span className="alert-mark" aria-hidden="true">
              <Icon name="history" className="h-[18px] w-[18px]" />
            </span>
            {e.status === 'queued' ? 'Menunggu worker mengambil job.' : 'Worker sedang menjalankan SQL.'} Halaman ini
            diperbarui otomatis.
          </p>
        )}
        {e.failure_message && (
          <p role="alert" className="alert alert-error">
            <span className="alert-mark" aria-hidden="true">
              !
            </span>
            <span className="min-w-0 break-words">
              {e.failure_message}
              <span className="mt-0.5 block text-xs opacity-75">kode: {e.failure_code}</span>
            </span>
          </p>
        )}
        {job_id && (
          <p className="m-0 text-[0.85rem] text-muted">
            Job ID <code className="break-all">{job_id}</code>
          </p>
        )}
        {can_edit && waiting && (
          <div className="flex flex-wrap gap-2.5">
            {e.status === 'approved' && (
              <button type="button" className="btn-primary" disabled={run.isPending} onClick={() => run.mutate()}>
                <Icon name="bolt" />
                Jalankan
              </button>
            )}
            <button
              type="button"
              className="btn-danger"
              disabled={cancel.isPending}
              onClick={() => window.confirm('Batalkan permintaan ini?') && cancel.mutate()}
            >
              Batalkan permintaan
            </button>
          </div>
        )}
      </div>

      <h2 className="text-[1.2rem]">Persetujuan leader</h2>
      <div className="mb-5 space-y-3">
        {approvals.map((a) => (
          <ApprovalCard
            key={a.group_id}
            approval={a}
            canDecide={canDecide(a)}
            busy={decide.isPending}
            onDecide={(approve, comment) => decide.mutate({ groupID: a.group_id, approve, comment })}
          />
        ))}
      </div>

      <section className="card mb-5">
        <SqlBlock title="SQL yang dijalankan" sql={e.sql} />
      </section>

      {attempts.length > 0 && (
        <>
          <h2 className="text-[1.2rem]">Percobaan</h2>
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>Ke</th>
                  <th>Hasil</th>
                  <th>Tercommit</th>
                  <th>Batch / baris</th>
                  <th>Selesai</th>
                  <th>Keterangan</th>
                </tr>
              </thead>
              <tbody>
                {attempts.map((a) => (
                  <tr key={a.id}>
                    <td className="font-bold">{a.attempt}</td>
                    <td>
                      <StatusBadge status={a.status} />
                    </td>
                    <td>{a.committed ? 'Ya' : 'Tidak'}</td>
                    <td className="tabular-nums">
                      {a.batches_committed} / {a.rows_committed}
                    </td>
                    <td className="whitespace-nowrap">{formatTime(a.finished_at)}</td>
                    <td className="min-w-[220px]">{a.failure_message || '–'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </>
  )
}

interface ApprovalCardProps {
  approval: Approval
  canDecide: boolean
  busy: boolean
  onDecide: (approve: boolean, comment: string) => void
}

function ApprovalCard({ approval: a, canDecide, busy, onDecide }: ApprovalCardProps) {
  const [comment, setComment] = useState('')
  return (
    <article className="card space-y-3 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span>
          <strong>{a.group_name}</strong>
          <span className="text-muted"> · leader {a.leader_name}</span>
          {a.auto && <span className="text-muted"> · otomatis karena pengaju adalah leader</span>}
        </span>
        <ApprovalBadge status={a.status} />
      </div>
      {a.comment && <p className="m-0 text-muted">“{a.comment}”</p>}
      {canDecide && (
        <>
          <textarea
            className="input"
            aria-label={`Komentar untuk ${a.group_name}`}
            placeholder="Komentar, wajib diisi kalau menolak"
            value={comment}
            onChange={(event) => setComment(event.target.value)}
          />
          <div className="flex flex-wrap gap-2.5">
            <button type="button" className="btn-primary" disabled={busy} onClick={() => onDecide(true, comment)}>
              Setujui
            </button>
            <button
              type="button"
              className="btn-danger"
              disabled={busy || !comment.trim()}
              onClick={() => onDecide(false, comment)}
            >
              Tolak
            </button>
          </div>
        </>
      )}
    </article>
  )
}
