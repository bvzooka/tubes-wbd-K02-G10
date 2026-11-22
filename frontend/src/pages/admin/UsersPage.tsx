import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { User } from '../../api/types'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'

export default function UsersPage() {
  const { data: users, isLoading, error } = useQuery({
    queryKey: ['admin', 'users'],
    queryFn: () => api<User[]>('GET', '/admin/users'),
  })

  return (
    <>
      <PageHeader
        title="User"
        action={
          <Link to="/admin/users/new" className="btn-primary">
            Tambah user
          </Link>
        }
      />
      {isLoading && <Loading />}
      <ErrorMessage error={error} />
      {users?.length === 0 && <EmptyState message="Belum ada user." />}
      {users && users.length > 0 && (
        <div className="card overflow-x-auto p-0">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-slate-200 bg-slate-50 text-slate-600">
              <tr>
                <th className="px-4 py-3">Username</th>
                <th className="px-4 py-3">Peran</th>
                <th className="px-4 py-3">Status</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id} className="border-b border-slate-100 last:border-0">
                  <td className="px-4 py-3">
                    <Link to={`/admin/users/${u.id}`} className="font-medium text-indigo-700 hover:underline">
                      {u.username}
                    </Link>
                  </td>
                  <td className="px-4 py-3">{u.is_platform_admin ? 'Platform Admin' : 'User'}</td>
                  <td className="px-4 py-3">{u.is_active ? 'Aktif' : 'Nonaktif'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
