import { Link, useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { Execution, ExecutionKind, Migration, MigrationDetail } from '../../api/types'
import Badge from '../../components/Badge'
import Breadcrumbs from '../../components/Breadcrumbs'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import { formatTime, isOpen, kindLabel, modeLabel, StateBadge, StatusBadge } from './labels'
import SqlBlock from './SqlBlock'
import { editMigrationPath, executionPath, migrationsKey, migrationsPath } from './paths'

type StatusAction = 'freeze' | 'cancel' | 'reopen' | 'supersede'

interface RequestChoice {
  label: string
  kind: ExecutionKind
  resume: boolean
  danger?: boolean
}

// permintaan yang ditawarkan mengikuti aturan di rules.go, backend tetap mengeceknya lagi
function requestChoices(m: Migration): RequestChoice[] {
  const rollback: RequestChoice = { label: 'Ajukan rollback', kind: 'rollback', resume: false, danger: true }
  const undo = m.irreversible ? [] : [rollback]
  switch (m.state) {
    case 'ready':
      return [{ label: 'Ajukan apply', kind: 'apply', resume: false }]
    case 'failed':
    case 'rolled_back':
      return [{ label: 'Ajukan forward', kind: 'forward', resume: false }]
    case 'applied':
      return undo
    case 'partial':
      return [{ label: 'Ajukan resume', kind: 'forward', resume: true }, ...undo]
    case 'partially_reverted':
      return m.irreversible ? [] : [{ ...rollback, label: 'Lanjutkan rollback' }]
    default:
      return []
  }
}

const confirmText: Partial<Record<StatusAction, string>> = {
  cancel: 'Batalkan draft ini? Versinya tidak bisa dipakai lagi.',
  supersede: 'Buang migration ini? Versinya tidak akan dicoba lagi.',
}

export default function MigrationDetailPage() {
  const { id = '', version = '' } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const base = `/databases/${id}/migrations/${version}`

  const detail = useQuery({
    queryKey: [...migrationsKey(id), version],
    queryFn: () => api<MigrationDetail>('GET', base),
    // selama masih ada permintaan terbuka, halaman ikut diperbarui
    refetchInterval: (query) => (query.state.data?.executions.some((e) => isOpen(e.status)) ? 3000 : false),
  })
  const refresh = () => queryClient.invalidateQueries({ queryKey: migrationsKey(id) })

  const change = useMutation({
    mutationFn: ({ action, revision }: { action: StatusAction; revision: number }) =>
      // hanya freeze yang membawa revisi, supaya yang dikunci sama dengan yang sedang dilihat
      api<Migration>('POST', `${base}/${action}`, action === 'freeze' ? { revision } : undefined),
    onSettled: refresh,
  })
  const request = useMutation({
    mutationFn: ({ kind, resume }: RequestChoice) => api<Execution>('POST', `${base}/executions`, { kind, resume }),
    onSuccess: (execution) => {
      refresh()
      navigate(executionPath(execution.id))
    },
  })

  if (detail.isLoading) return <Loading />
  if (!detail.data) return <ErrorMessage error={detail.error} />
  const { migration: m, executions, can_edit } = detail.data
  const open = executions.find((e) => isOpen(e.status))
  const busy = change.isPending || request.isPending

  function run(action: StatusAction) {
    const question = confirmText[action]
    if (question && !window.confirm(question)) return
    change.mutate({ action, revision: m.revision })
  }

  return (
    <>
      <Breadcrumbs
        items={[{ label: 'Database', to: '/databases' }, { label: 'Migration', to: migrationsPath(id) }, { label: `v${m.version}` }]}
      />
      <PageHeader
        eyebrow={{ icon: 'history', label: `Versi ${m.version} · revisi ${m.revision}` }}
        title={m.name}
        titleExtra={
          <>
            <StateBadge state={m.state} />
            <Badge tone="outline">{modeLabel[m.mode]}</Badge>
          </>
        }
        subtitle={`Dibuat ${m.created_by}, diubah ${formatTime(m.updated_at)}`}
      />

      <div className="mb-4 space-y-3">
        <ErrorMessage error={change.error ?? request.error} />
        {open && (
          <p className="alert alert-warning">
            <span className="alert-mark" aria-hidden="true">
              !
            </span>
            <span>
              Masih ada permintaan yang belum selesai. <Link to={executionPath(open.id)}>Buka permintaan #{open.id}</Link>
            </span>
          </p>
        )}
        {can_edit && !open && (
          <div className="flex flex-wrap gap-2.5">
            {m.state === 'draft' && (
              <>
                <button type="button" className="btn-primary" disabled={busy} onClick={() => run('freeze')}>
                  <Icon name="lock" />
                  Bekukan
                </button>
                <Link to={editMigrationPath(id, m.version)} className="btn-secondary">
                  <Icon name="edit" />
                  Ubah
                </Link>
                <button type="button" className="btn-danger" disabled={busy} onClick={() => run('cancel')}>
                  Batalkan draft
                </button>
              </>
            )}
            {requestChoices(m).map((choice) => (
              <button
                key={choice.label}
                type="button"
                className={choice.danger ? 'btn-danger' : 'btn-primary'}
                disabled={busy}
                onClick={() => request.mutate(choice)}
              >
                <Icon name="bolt" />
                {choice.label}
              </button>
            ))}
            {m.state === 'ready' && (
              <button type="button" className="btn-secondary" disabled={busy} onClick={() => run('reopen')}>
                Buka lagi
              </button>
            )}
            {(m.state === 'failed' || m.state === 'rolled_back') && (
              <button type="button" className="btn-secondary" disabled={busy} onClick={() => run('supersede')}>
                Supersede
              </button>
            )}
          </div>
        )}
      </div>

      <section className="card mb-5 space-y-4">
        <SqlBlock title="SQL up" sql={m.sql_up} />
        {m.irreversible ? (
          <p className="m-0">
            <strong>Irreversible:</strong> {m.irreversible_reason || 'alasan belum diisi'}
          </p>
        ) : (
          <SqlBlock title="SQL down" sql={m.sql_down} />
        )}
        {m.batch && (
          <p className="m-0 text-muted">
            Batch {m.batch.batch_size} baris pada <code>{`${m.batch.schema}.${m.batch.table}`}</code> menurut kolom{' '}
            <code>{m.batch.key_column}</code>
          </p>
        )}
      </section>

      <h2 className="text-[1.2rem]">Permintaan eksekusi</h2>
      {executions.length === 0 ? (
        <EmptyState icon="history" message="Belum pernah diajukan." />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>ID</th>
                <th>Jenis</th>
                <th>Status</th>
                <th>Pengaju</th>
                <th>Diajukan</th>
              </tr>
            </thead>
            <tbody>
              {executions.map((e) => (
                <tr key={e.id}>
                  <td>
                    <Link to={executionPath(e.id)} className="font-bold">
                      #{e.id}
                    </Link>
                  </td>
                  <td>{e.resumes_execution_id ? 'Resume' : kindLabel[e.kind]}</td>
                  <td>
                    <StatusBadge status={e.status} />
                  </td>
                  <td className="whitespace-nowrap">{e.requested_by}</td>
                  <td className="whitespace-nowrap">{formatTime(e.requested_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
