import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '../../api/client'
import type { Group } from '../../api/types'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import FieldError from '../../components/FieldError'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'

export default function GroupsPage() {
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')

  const groups = useQuery({
    queryKey: ['admin', 'groups'],
    queryFn: () => api<Group[]>('GET', '/admin/groups'),
  })
  const create = useMutation({
    mutationFn: () => api<Group>('POST', '/admin/groups', { name, description }),
    onSuccess: () => {
      setName('')
      setDescription('')
      queryClient.invalidateQueries({ queryKey: ['admin', 'groups'] })
    },
  })
  const fields = create.error instanceof ApiError ? create.error.fields : {}

  function submit(e: FormEvent) {
    e.preventDefault()
    create.mutate()
  }

  return (
    <>
      <PageHeader title="Group" />
      <form onSubmit={submit} className="card mb-4 grid gap-3 sm:grid-cols-[1fr_2fr_auto] sm:items-start">
        <div>
          <input className="input" placeholder="Nama group" value={name} onChange={(e) => setName(e.target.value)} required />
          <FieldError message={fields.name} />
        </div>
        <div>
          <input
            className="input"
            placeholder="Deskripsi (opsional)"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
          <FieldError message={fields.description} />
        </div>
        <button className="btn-primary" disabled={create.isPending}>
          Tambah
        </button>
      </form>

      {groups.isLoading && <Loading />}
      <ErrorMessage error={groups.error} />
      {groups.data?.length === 0 && <EmptyState message="Belum ada group." />}
      {groups.data && groups.data.length > 0 && (
        <div className="card overflow-x-auto p-0">
          <table className="w-full text-left text-sm">
            <thead className="border-b border-slate-200 bg-slate-50 text-slate-600">
              <tr>
                <th className="px-4 py-3">Nama</th>
                <th className="px-4 py-3">Leader</th>
                <th className="px-4 py-3">Anggota</th>
                <th className="px-4 py-3">Akses database</th>
              </tr>
            </thead>
            <tbody>
              {groups.data.map((g) => (
                <tr key={g.id} className="border-b border-slate-100 last:border-0">
                  <td className="px-4 py-3">
                    <Link to={`/admin/groups/${g.id}`} className="font-medium text-indigo-700 hover:underline">
                      {g.name}
                    </Link>
                  </td>
                  <td className="px-4 py-3">{g.leader_name || <span className="text-amber-600">Belum ada</span>}</td>
                  <td className="px-4 py-3">{g.member_count}</td>
                  <td className="px-4 py-3">{g.access_count}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
