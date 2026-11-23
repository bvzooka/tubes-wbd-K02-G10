import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { Connection } from '../../api/types'
import Badge from '../../components/Badge'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import StatCard from '../../components/StatCard'
import { dbmsLabel } from '../databases/paths'
import { connectionsKey } from './keys'

export default function ConnectionsPage() {
  const queryClient = useQueryClient()
  const { data: conns, isLoading, error } = useQuery({
    queryKey: connectionsKey,
    queryFn: () => api<Connection[]>('GET', '/admin/connections'),
  })

  // Status aktif memengaruhi daftar database user lain juga, jadi cache viewer ikut dibuang.
  const toggle = useMutation({
    mutationFn: (c: Connection) =>
      api<Connection>('POST', `/admin/connections/${c.id}/${c.is_active ? 'deactivate' : 'activate'}`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: connectionsKey })
      queryClient.invalidateQueries({ queryKey: ['databases'] })
    },
  })

  const addButton = (
    <Link to="/admin/connections/new" className="btn-primary">
      <Icon name="plus" />
      Tambah koneksi
    </Link>
  )

  return (
    <>
      <PageHeader
        eyebrow={{ icon: 'plug', label: 'Administrasi' }}
        title={
          <>
            Koneksi <strong className="font-bold">database</strong>
          </>
        }
        subtitle="Server database target. Password disimpan terenkripsi dan tidak pernah ditampilkan."
        action={addButton}
        aside={conns && <StatCard icon="server" label="Total koneksi" value={conns.length} />}
      />

      {isLoading && <Loading />}
      {(error ?? toggle.error) && (
        <div className="mb-4">
          <ErrorMessage error={error ?? toggle.error} />
        </div>
      )}

      {conns?.length === 0 && (
        <EmptyState
          icon="plug"
          title="Belum ada koneksi"
          message="Tambahkan koneksi pertama supaya database target bisa diberikan ke group."
          action={addButton}
        />
      )}

      {conns && conns.length > 0 && (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>Koneksi</th>
                <th>DBMS</th>
                <th>Alamat</th>
                <th>User</th>
                <th>Status</th>
                <th>Aksi</th>
              </tr>
            </thead>
            <tbody>
              {conns.map((c) => (
                <tr key={c.id}>
                  <td>
                    <span className="flex items-center gap-3 whitespace-nowrap font-bold">
                      <span className="icon-bubble h-[38px] w-[38px]">
                        <Icon name="db" className="h-[18px] w-[18px]" />
                      </span>
                      {c.name}
                    </span>
                  </td>
                  <td>
                    <Badge tone="outline">{dbmsLabel[c.dbms] ?? c.dbms}</Badge>
                  </td>
                  {/* Host, port, database, dan SSL digabung supaya tabel muat di samping sidebar. */}
                  <td className="whitespace-nowrap">
                    <code>
                      {c.host}:{c.port}/{c.database_name}
                    </code>
                    <span className="mt-1 block text-[0.78rem] text-muted">SSL {c.ssl_mode}</span>
                  </td>
                  <td className="whitespace-nowrap">{c.db_username}</td>
                  <td>
                    <Badge tone={c.is_active ? 'green' : 'neutral'} dot>
                      {c.is_active ? 'Aktif' : 'Nonaktif'}
                    </Badge>
                  </td>
                  <td>
                    <div className="flex items-center gap-4 whitespace-nowrap">
                      <Link to={`/admin/connections/${c.id}`} className="btn-link" aria-label={`Ubah ${c.name}`}>
                        <Icon name="edit" />
                        Ubah
                      </Link>
                      <button
                        type="button"
                        className={'btn-link' + (c.is_active ? ' destructive' : '')}
                        disabled={toggle.isPending}
                        onClick={() => toggle.mutate(c)}
                        aria-label={`${c.is_active ? 'Nonaktifkan' : 'Aktifkan'} ${c.name}`}
                      >
                        {c.is_active ? 'Nonaktifkan' : 'Aktifkan'}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
