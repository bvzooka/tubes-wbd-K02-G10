import { useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { AccessLevel, GroupDetail } from '../../api/types'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'

const levelLabel: Record<AccessLevel, string> = { view_only: 'View only', view_edit: 'View + Edit' }

export default function GroupDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [newMember, setNewMember] = useState('')
  const [newConn, setNewConn] = useState('')
  const [newLevel, setNewLevel] = useState<AccessLevel>('view_only')

  const key = ['admin', 'groups', id]
  const group = useQuery({ queryKey: key, queryFn: () => api<GroupDetail>('GET', `/admin/groups/${id}`) })

  // Semua aksi di halaman ini memuat ulang detail group setelah berhasil.
  const action = useMutation({
    mutationFn: (run: () => Promise<void>) => run(),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'groups'] }),
  })
  const remove = useMutation({
    mutationFn: () => api('DELETE', `/admin/groups/${id}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'groups'] })
      navigate('/admin/groups')
    },
  })

  if (group.isLoading) return <Loading />
  if (!group.data) return <ErrorMessage error={group.error} />
  const g = group.data

  return (
    <>
      <PageHeader
        title={g.name}
        action={
          g.can_delete && (
            <button className="btn-danger" onClick={() => remove.mutate()}>
              Hapus group
            </button>
          )
        }
      />
      {g.description && <p className="mb-4 text-sm text-slate-600">{g.description}</p>}
      <div className="mb-4">
        <ErrorMessage error={action.error ?? remove.error} />
      </div>

      <section className="card mb-4">
        <h2 className="mb-3 font-medium">Anggota</h2>
        {!g.leader_id && (
          <p className="mb-3 rounded-md bg-amber-50 p-2 text-sm text-amber-700">
            Group ini belum punya leader. Permintaan migration pada database yang diakses group ini akan ditolak.
          </p>
        )}
        {g.members.length === 0 && <EmptyState message="Belum ada anggota." />}
        <ul className="divide-y divide-slate-100">
          {g.members.map((m) => (
            <li key={m.user_id} className="flex flex-wrap items-center justify-between gap-2 py-2 text-sm">
              <span>
                {m.username}
                {m.is_leader && <span className="ml-2 rounded bg-indigo-100 px-2 py-0.5 text-xs text-indigo-700">Leader</span>}
                {!m.is_active && <span className="ml-2 text-xs text-slate-500">(nonaktif)</span>}
              </span>
              {!m.is_leader && (
                <span className="flex gap-2">
                  <button
                    className="btn-secondary"
                    onClick={() => action.mutate(() => api('PUT', `/admin/groups/${id}/leader`, { user_id: m.user_id }))}
                  >
                    Jadikan leader
                  </button>
                  <button
                    className="btn-secondary"
                    onClick={() => action.mutate(() => api('DELETE', `/admin/groups/${id}/members/${m.user_id}`))}
                  >
                    Keluarkan
                  </button>
                </span>
              )}
            </li>
          ))}
        </ul>
        {g.available_users.length > 0 && (
          <div className="mt-3 flex flex-wrap gap-2">
            <select className="input sm:w-64" value={newMember} onChange={(e) => setNewMember(e.target.value)}>
              <option value="">Pilih user…</option>
              {g.available_users.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.name}
                </option>
              ))}
            </select>
            <button
              className="btn-primary"
              disabled={!newMember}
              onClick={() =>
                action.mutate(async () => {
                  await api('POST', `/admin/groups/${id}/members`, { user_id: Number(newMember) })
                  setNewMember('')
                })
              }
            >
              Tambah anggota
            </button>
          </div>
        )}
      </section>

      <section className="card">
        <h2 className="mb-3 font-medium">Akses database</h2>
        {g.accesses.length === 0 && <EmptyState message="Belum ada akses database." />}
        <ul className="divide-y divide-slate-100">
          {g.accesses.map((a) => (
            <li key={a.connection_id} className="flex flex-wrap items-center justify-between gap-2 py-2 text-sm">
              <span>
                {a.connection_name} <span className="text-slate-500">· {levelLabel[a.access_level]}</span>
              </span>
              <button
                className="btn-secondary"
                onClick={() => action.mutate(() => api('DELETE', `/admin/groups/${id}/access/${a.connection_id}`))}
              >
                Cabut
              </button>
            </li>
          ))}
        </ul>
        <div className="mt-3 flex flex-wrap gap-2">
          <select className="input sm:w-64" value={newConn} onChange={(e) => setNewConn(e.target.value)}>
            <option value="">Pilih koneksi…</option>
            {g.available_connections.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
          <select className="input sm:w-40" value={newLevel} onChange={(e) => setNewLevel(e.target.value as AccessLevel)}>
            <option value="view_only">{levelLabel.view_only}</option>
            <option value="view_edit">{levelLabel.view_edit}</option>
          </select>
          <button
            className="btn-primary"
            disabled={!newConn}
            onClick={() =>
              action.mutate(async () => {
                await api('PUT', `/admin/groups/${id}/access/${newConn}`, { access_level: newLevel })
                setNewConn('')
              })
            }
          >
            Beri / ubah akses
          </button>
        </div>
      </section>
    </>
  )
}
