import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { Group } from '../../api/types'
import Badge from '../../components/Badge'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import StatCard from '../../components/StatCard'
import { formatDateTime } from '../../format'

export default function GroupsPage() {
  const { data: groups, isLoading, error } = useQuery({
    queryKey: ['admin', 'groups'],
    queryFn: () => api<Group[]>('GET', '/admin/groups'),
  })

  const addButton = (
    <Link to="/admin/groups/new" className="btn-primary">
      <Icon name="plus" />
      Tambah group
    </Link>
  )
  const withoutLeader = groups?.filter((g) => !g.leader_name).length ?? 0

  return (
    <>
      <PageHeader
        eyebrow={{ icon: 'grid', label: 'Administrasi' }}
        title="Group"
        subtitle="Group menentukan siapa boleh melihat atau mengubah database. Setiap group butuh leader untuk menyetujui migration."
        action={addButton}
        aside={groups && <StatCard icon="grid" label="Total group" value={groups.length} />}
      />

      {isLoading && <Loading />}
      {error && (
        <div className="mb-4">
          <ErrorMessage error={error} />
        </div>
      )}

      {withoutLeader > 0 && (
        <p className="alert alert-warning mb-4">
          <span className="alert-mark" aria-hidden="true">
            !
          </span>
          {withoutLeader} group belum punya leader. Permintaan migration ke database yang diakses group itu akan
          tertahan sampai leader ditetapkan.
        </p>
      )}

      {groups?.length === 0 && (
        <EmptyState
          icon="grid"
          title="Belum ada group"
          message="Buat group, masukkan anggota, lalu beri akses database."
          action={addButton}
        />
      )}

      {groups && groups.length > 0 && (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>Group</th>
                <th>Leader</th>
                <th className="text-right">Anggota</th>
                <th className="text-right">Akses database</th>
                <th>Dibuat</th>
              </tr>
            </thead>
            <tbody>
              {groups.map((g) => (
                <tr key={g.id}>
                  <td>
                    <span className="flex items-center gap-3">
                      <span className="icon-bubble h-[38px] w-[38px]">
                        <Icon name="grid" className="h-[18px] w-[18px]" />
                      </span>
                      <span className="min-w-0">
                        <Link to={`/admin/groups/${g.id}`} className="whitespace-nowrap font-bold">
                          {g.name}
                        </Link>
                        {g.description && (
                          <span className="block max-w-[28ch] truncate text-[0.84rem] text-muted" title={g.description}>
                            {g.description}
                          </span>
                        )}
                      </span>
                    </span>
                  </td>
                  <td className="whitespace-nowrap">
                    {g.leader_name ? (
                      <Badge tone="ink">{g.leader_name}</Badge>
                    ) : (
                      <Badge tone="amber" dot>
                        Belum ada
                      </Badge>
                    )}
                  </td>
                  <td className="text-right tabular-nums">{g.member_count}</td>
                  <td className="text-right tabular-nums">{g.access_count}</td>
                  <td className="whitespace-nowrap text-muted">{formatDateTime(g.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
