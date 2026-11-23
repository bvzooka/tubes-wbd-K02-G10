import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { User } from '../../api/types'
import Badge from '../../components/Badge'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import StatCard from '../../components/StatCard'
import { formatDateTime } from '../../format'
import { useSession } from '../../hooks/useSession'

export default function UsersPage() {
  const queryClient = useQueryClient()
  const { data: session } = useSession()
  const { data: users, isLoading, error } = useQuery({
    queryKey: ['admin', 'users'],
    queryFn: () => api<User[]>('GET', '/admin/users'),
  })

  const toggle = useMutation({
    mutationFn: (u: User) => api<User>('POST', `/admin/users/${u.id}/${u.is_active ? 'deactivate' : 'activate'}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin', 'users'] }),
  })

  const addButton = (
    <Link to="/admin/users/new" className="btn-primary">
      <Icon name="plus" />
      Tambah pengguna
    </Link>
  )

  return (
    <>
      <PageHeader
        eyebrow={{ icon: 'users', label: 'Administrasi' }}
        title="Pengguna"
        subtitle="Akun yang bisa masuk ke platform. Akses database diberikan lewat group, bukan per pengguna."
        action={addButton}
        aside={users && <StatCard icon="users" label="Total pengguna" value={users.length} />}
      />

      {isLoading && <Loading />}
      {(error ?? toggle.error) && (
        <div className="mb-4">
          <ErrorMessage error={error ?? toggle.error} />
        </div>
      )}

      {users?.length === 0 && (
        <EmptyState icon="users" title="Belum ada pengguna" message="Tambahkan pengguna pertama." action={addButton} />
      )}

      {users && users.length > 0 && (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>Username</th>
                <th>Peran</th>
                <th>Status</th>
                <th>Dibuat</th>
                <th>Aksi</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => {
                const isSelf = u.id === session?.user.id
                return (
                  <tr key={u.id}>
                    <td>
                      <span className="flex items-center gap-3 whitespace-nowrap font-bold">
                        <span className="icon-bubble h-[38px] w-[38px]">
                          <Icon name="users" className="h-[18px] w-[18px]" />
                        </span>
                        {u.username}
                        {isSelf && <span className="font-normal text-muted">(kamu)</span>}
                      </span>
                    </td>
                    <td>
                      {u.is_platform_admin ? <Badge tone="lime">Platform Admin</Badge> : <Badge>Pengguna</Badge>}
                    </td>
                    <td>
                      <Badge tone={u.is_active ? 'green' : 'neutral'} dot>
                        {u.is_active ? 'Aktif' : 'Nonaktif'}
                      </Badge>
                    </td>
                    <td className="whitespace-nowrap text-muted">{formatDateTime(u.created_at)}</td>
                    <td>
                      <div className="flex items-center gap-4 whitespace-nowrap">
                        <Link to={`/admin/users/${u.id}`} className="btn-link" aria-label={`Ubah ${u.username}`}>
                          <Icon name="edit" />
                          Ubah
                        </Link>
                        {/* Akun sendiri tidak bisa dinonaktifkan (backend juga menolaknya). */}
                        {!isSelf && (
                          <button
                            type="button"
                            className={'btn-link' + (u.is_active ? ' destructive' : '')}
                            disabled={toggle.isPending}
                            onClick={() => toggle.mutate(u)}
                            aria-label={`${u.is_active ? 'Nonaktifkan' : 'Aktifkan'} ${u.username}`}
                          >
                            {u.is_active ? 'Nonaktifkan' : 'Aktifkan'}
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
