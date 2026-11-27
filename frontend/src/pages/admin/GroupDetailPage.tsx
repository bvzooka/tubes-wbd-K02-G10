import { useEffect, useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '../../api/client'
import type { AccessLevel, Group, GroupDetail } from '../../api/types'
import Badge from '../../components/Badge'
import Breadcrumbs from '../../components/Breadcrumbs'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Field from '../../components/Field'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import Panel from '../../components/Panel'
import StatCard from '../../components/StatCard'
import { accessLabel } from '../databases/paths'

const otherLevel: Record<AccessLevel, AccessLevel> = { view_only: 'view_edit', view_edit: 'view_only' }

export default function GroupDetailPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [newMember, setNewMember] = useState('')
  const [newConn, setNewConn] = useState('')
  const [newLevel, setNewLevel] = useState<AccessLevel>('view_only')
  const [confirmDelete, setConfirmDelete] = useState(false)

  const group = useQuery({
    queryKey: ['admin', 'groups', id],
    queryFn: () => api<GroupDetail>('GET', `/admin/groups/${id}`),
  })
  useEffect(() => {
    if (group.data) {
      setName(group.data.name)
      setDescription(group.data.description)
    }
  }, [group.data])

  // Anggota, leader, dan akses memakai satu mutation: setelah berhasil, detail dimuat ulang.
  // Daftar database ikut dibuang dari cache karena akses admin sendiri bisa berubah.
  const action = useMutation({
    mutationFn: (run: () => Promise<unknown>) => run(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'groups'] })
      queryClient.invalidateQueries({ queryKey: ['databases'] })
    },
  })
  const saveDetail = useMutation({
    mutationFn: () => api<Group>('PUT', `/admin/groups/${id}`, { name, description }),
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
  const busy = action.isPending
  const detailFields = saveDetail.error instanceof ApiError ? saveDetail.error.fields : {}
  const detailError = saveDetail.error instanceof ApiError && saveDetail.error.status === 422 ? null : saveDetail.error

  function run(fn: () => Promise<unknown>) {
    action.mutate(fn)
  }

  function submitDetail(e: FormEvent) {
    e.preventDefault()
    saveDetail.mutate()
  }

  return (
    <>
      <Breadcrumbs items={[{ label: 'Group', to: '/admin/groups' }, { label: g.name }]} />
      <PageHeader
        eyebrow={{ icon: 'grid', label: 'Group' }}
        title={g.name}
        titleExtra={
          g.leader_name ? (
            <Badge tone="ink">Leader: {g.leader_name}</Badge>
          ) : (
            <Badge tone="amber" dot>
              Belum ada leader
            </Badge>
          )
        }
        subtitle={g.description || undefined}
        aside={
          <>
            <StatCard icon="users" label="Anggota" value={g.member_count} />
            <StatCard icon="db" label="Akses database" value={g.access_count} />
          </>
        }
      />

      <div className="space-y-4">
        {!g.leader_id && (
          <p className="alert alert-warning">
            <span className="alert-mark" aria-hidden="true">
              !
            </span>
            Group ini belum punya leader. Permintaan migration ke database yang diakses group ini akan tertahan sampai
            leader ditetapkan.
          </p>
        )}
        {action.error && <ErrorMessage error={action.error} />}
      </div>

      <div className="mt-5">
        <Panel
          icon="users"
          title="Anggota"
          count={g.members.length}
          footer={
            g.available_users.length > 0 ? (
              <form
                className="flex flex-wrap items-end gap-2.5 max-sm:[&>label]:basis-full"
                onSubmit={(e) => {
                  e.preventDefault()
                  run(async () => {
                    await api('POST', `/admin/groups/${id}/members`, { user_id: Number(newMember) })
                    setNewMember('')
                  })
                }}
              >
                <Field label="Tambah anggota">
                  <select
                    className="input sm:w-72"
                    value={newMember}
                    onChange={(e) => setNewMember(e.target.value)}
                    required
                  >
                    <option value="">Pilih pengguna…</option>
                    {g.available_users.map((u) => (
                      <option key={u.id} value={u.id}>
                        {u.name}
                      </option>
                    ))}
                  </select>
                </Field>
                <button type="submit" className="btn-primary" disabled={!newMember || busy}>
                  <Icon name="plus" />
                  Tambahkan
                </button>
              </form>
            ) : (
              <p className="m-0 text-sm text-muted">Semua pengguna aktif sudah menjadi anggota group ini.</p>
            )
          }
        >
          {g.members.length === 0 ? (
            <EmptyState plain icon="users" message="Belum ada anggota. Tambahkan pengguna di bawah." />
          ) : (
            <table className="table">
              <thead>
                <tr>
                  <th>Username</th>
                  <th>Status</th>
                  <th>Peran di group</th>
                  <th>Aksi</th>
                </tr>
              </thead>
              <tbody>
                {g.members.map((m) => (
                  <tr key={m.user_id}>
                    <td className="whitespace-nowrap font-bold">{m.username}</td>
                    <td>
                      <Badge tone={m.is_active ? 'green' : 'neutral'} dot>
                        {m.is_active ? 'Aktif' : 'Nonaktif'}
                      </Badge>
                    </td>
                    <td>{m.is_leader ? <Badge tone="ink">Leader</Badge> : <span className="text-muted">Anggota</span>}</td>
                    <td>
                      {m.is_leader ? (
                        // Backend menolak mengeluarkan leader; leader harus diganti lebih dulu.
                        <span className="text-[0.84rem] text-muted">Ganti leader dulu untuk mengeluarkan</span>
                      ) : (
                        <div className="flex items-center gap-4 whitespace-nowrap">
                          {m.is_active && (
                            <button
                              type="button"
                              className="btn-link"
                              disabled={busy}
                              onClick={() => run(() => api('PUT', `/admin/groups/${id}/leader`, { user_id: m.user_id }))}
                              aria-label={`Jadikan leader ${m.username}`}
                            >
                              <Icon name="shield" />
                              Jadikan leader
                            </button>
                          )}
                          <button
                            type="button"
                            className="btn-link destructive"
                            disabled={busy}
                            onClick={() => run(() => api('DELETE', `/admin/groups/${id}/members/${m.user_id}`))}
                            aria-label={`Keluarkan ${m.username}`}
                          >
                            Keluarkan
                          </button>
                        </div>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>

        <Panel
          icon="db"
          title="Akses database"
          count={g.accesses.length}
          footer={
            g.available_connections.length > 0 ? (
              <form
                className="flex flex-wrap items-end gap-2.5 max-sm:[&>label]:basis-full"
                onSubmit={(e) => {
                  e.preventDefault()
                  run(async () => {
                    await api('PUT', `/admin/groups/${id}/access/${newConn}`, { access_level: newLevel })
                    setNewConn('')
                  })
                }}
              >
                <Field label="Database">
                  <select className="input sm:w-64" value={newConn} onChange={(e) => setNewConn(e.target.value)} required>
                    <option value="">Pilih database…</option>
                    {g.available_connections.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.name}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label="Level akses">
                  <select
                    className="input sm:w-44"
                    value={newLevel}
                    onChange={(e) => setNewLevel(e.target.value as AccessLevel)}
                  >
                    <option value="view_only">{accessLabel.view_only}</option>
                    <option value="view_edit">{accessLabel.view_edit}</option>
                  </select>
                </Field>
                <button type="submit" className="btn-primary" disabled={!newConn || busy}>
                  <Icon name="check" />
                  Simpan akses
                </button>
                <span className="hint basis-full">
                  Memilih database yang sudah punya akses akan mengubah levelnya.
                </span>
              </form>
            ) : (
              <p className="m-0 text-sm text-muted">Belum ada koneksi database yang aktif.</p>
            )
          }
        >
          {g.accesses.length === 0 ? (
            <EmptyState plain icon="db" message="Group ini belum punya akses ke database mana pun." />
          ) : (
            <table className="table">
              <thead>
                <tr>
                  <th>Database</th>
                  <th>Level</th>
                  <th>Aksi</th>
                </tr>
              </thead>
              <tbody>
                {g.accesses.map((a) => (
                  <tr key={a.connection_id}>
                    <td className="whitespace-nowrap font-bold">{a.connection_name}</td>
                    <td>
                      <Badge tone={a.access_level === 'view_edit' ? 'lime' : 'butter'}>
                        {accessLabel[a.access_level]}
                      </Badge>
                    </td>
                    <td>
                      <div className="flex items-center gap-4 whitespace-nowrap">
                        <button
                          type="button"
                          className="btn-link"
                          disabled={busy}
                          onClick={() =>
                            run(() =>
                              api('PUT', `/admin/groups/${id}/access/${a.connection_id}`, {
                                access_level: otherLevel[a.access_level],
                              }),
                            )
                          }
                          // nama database ikut disebut supaya tombol tiap baris bisa dibedakan pembaca layar
                          aria-label={`Ubah ke ${accessLabel[otherLevel[a.access_level]]} untuk ${a.connection_name}`}
                        >
                          <Icon name="edit" />
                          Ubah ke {accessLabel[otherLevel[a.access_level]]}
                        </button>
                        <button
                          type="button"
                          className="btn-link destructive"
                          disabled={busy}
                          onClick={() => run(() => api('DELETE', `/admin/groups/${id}/access/${a.connection_id}`))}
                          aria-label={`Cabut akses ${a.connection_name}`}
                        >
                          Cabut
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>

        <Panel icon="edit" title="Detail group">
          <form onSubmit={submitDetail} className="space-y-4 px-6 py-5 max-md:px-4" noValidate>
            <ErrorMessage error={detailError} />
            <Field label="Nama group" error={detailFields.name}>
              <input className="input" value={name} onChange={(e) => setName(e.target.value)} maxLength={64} required />
            </Field>
            <Field label="Deskripsi" error={detailFields.description}>
              <textarea
                className="input"
                rows={3}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                maxLength={500}
              />
            </Field>
            <div className="flex flex-wrap items-center gap-3">
              <button type="submit" className="btn-primary" disabled={saveDetail.isPending}>
                <Icon name="check" />
                {saveDetail.isPending ? 'Menyimpan…' : 'Simpan perubahan'}
              </button>
              {saveDetail.isSuccess && (
                <span role="status" className="text-sm font-semibold text-green">
                  Tersimpan.
                </span>
              )}
            </div>
          </form>
        </Panel>

        <Panel icon="alert" title="Hapus group" danger>
          <div className="px-6 py-5 max-md:px-4">
            {g.can_delete ? (
              <div className="flex flex-wrap items-center gap-3">
                {/* Dua langkah, tanpa dialog browser: klik pertama meminta konfirmasi. */}
                {confirmDelete ? (
                  <>
                    <span className="text-sm font-semibold text-danger-ink">Hapus group “{g.name}” secara permanen?</span>
                    <button
                      type="button"
                      className="btn-danger"
                      disabled={remove.isPending}
                      onClick={() => remove.mutate()}
                    >
                      Ya, hapus
                    </button>
                    <button type="button" className="btn-secondary" onClick={() => setConfirmDelete(false)}>
                      Batal
                    </button>
                  </>
                ) : (
                  <button type="button" className="btn-danger" onClick={() => setConfirmDelete(true)}>
                    Hapus group
                  </button>
                )}
              </div>
            ) : (
              <p className="m-0 text-sm text-muted">
                Group yang masih punya anggota atau akses database tidak bisa dihapus. Kosongkan dulu.
              </p>
            )}
            {remove.error && (
              <div className="mt-3">
                <ErrorMessage error={remove.error} />
              </div>
            )}
          </div>
        </Panel>
      </div>
    </>
  )
}
