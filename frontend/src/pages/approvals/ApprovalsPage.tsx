import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { InboxItem } from '../../api/types'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'

const kindLabel: Record<InboxItem['kind'], string> = { apply: 'Apply', rollback: 'Rollback', forward: 'Forward' }

interface Decision {
  item: InboxItem
  approve: boolean
  comment: string
}

export default function ApprovalsPage() {
  const queryClient = useQueryClient()
  const [rejecting, setRejecting] = useState<InboxItem | null>(null)
  const [comment, setComment] = useState('')

  const inbox = useQuery({ queryKey: ['approvals'], queryFn: () => api<InboxItem[]>('GET', '/approvals') })
  const decide = useMutation({
    mutationFn: ({ item, approve, comment }: Decision) =>
      api('POST', `/executions/${item.execution_id}/approvals/${item.group_id}/${approve ? 'approve' : 'reject'}`, { comment }),
    onSuccess: () => {
      setRejecting(null)
      setComment('')
      queryClient.invalidateQueries({ queryKey: ['approvals'] })
    },
  })

  return (
    <>
      <PageHeader title="Menunggu persetujuan saya" />
      {inbox.isLoading && <Loading />}
      <ErrorMessage error={inbox.error ?? decide.error} />
      {inbox.data?.length === 0 && <EmptyState message="Tidak ada permintaan yang menunggu keputusan Anda." />}

      <div className="space-y-4">
        {inbox.data?.map((item) => (
          <article key={`${item.execution_id}-${item.group_id}`} className="card space-y-3">
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <h2 className="font-medium">
                {kindLabel[item.kind]} v{item.version} · {item.database}
              </h2>
              <span className="text-xs text-muted">
                oleh {item.requested_by}, {new Date(item.requested_at).toLocaleString('id-ID')} · sebagai leader {item.group_name}
              </span>
            </div>
            <pre className="max-h-64 overflow-auto rounded-md bg-ink p-3 text-xs text-paper-soft">{item.sql}</pre>

            {rejecting === item ? (
              <div className="space-y-2">
                <textarea
                  className="input min-h-[88px] py-2"
                  placeholder="Alasan penolakan (wajib)"
                  value={comment}
                  onChange={(e) => setComment(e.target.value)}
                />
                <div className="flex flex-wrap gap-2">
                  <button
                    className="btn-danger"
                    disabled={!comment.trim() || decide.isPending}
                    onClick={() => decide.mutate({ item, approve: false, comment })}
                  >
                    Tolak
                  </button>
                  <button className="btn-secondary" onClick={() => setRejecting(null)}>
                    Batal
                  </button>
                </div>
              </div>
            ) : (
              <div className="flex flex-wrap gap-2">
                <button
                  className="btn-primary"
                  disabled={decide.isPending}
                  onClick={() => decide.mutate({ item, approve: true, comment: '' })}
                >
                  Setujui
                </button>
                <button className="btn-secondary" onClick={() => setRejecting(item)}>
                  Tolak…
                </button>
                <Link to={`/executions/${item.execution_id}`} className="btn-secondary">
                  Detail
                </Link>
              </div>
            )}
          </article>
        ))}
      </div>
    </>
  )
}
