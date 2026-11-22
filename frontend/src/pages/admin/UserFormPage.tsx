import { useEffect, useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '../../api/client'
import type { User } from '../../api/types'
import ErrorMessage from '../../components/ErrorMessage'
import FieldError from '../../components/FieldError'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'

// UserFormPage dipakai untuk /admin/users/new (buat) dan /admin/users/:id (ubah).
export default function UserFormPage() {
  const { id } = useParams()
  const isNew = id === undefined
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [isAdmin, setIsAdmin] = useState(false)

  const user = useQuery({
    queryKey: ['admin', 'users', id],
    queryFn: () => api<User>('GET', `/admin/users/${id}`),
    enabled: !isNew,
  })
  useEffect(() => {
    if (user.data) {
      setUsername(user.data.username)
      setIsAdmin(user.data.is_platform_admin)
    }
  }, [user.data])

  const save = useMutation({
    mutationFn: () =>
      isNew
        ? api<User>('POST', '/admin/users', { username, password, is_platform_admin: isAdmin })
        : api<User>('PUT', `/admin/users/${id}`, { password, is_platform_admin: isAdmin }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
      navigate('/admin/users')
    },
  })

  const setActive = useMutation({
    mutationFn: (active: boolean) =>
      api<User>('POST', `/admin/users/${id}/${active ? 'activate' : 'deactivate'}`),
    onSuccess: (u) => {
      queryClient.setQueryData(['admin', 'users', id], u)
      queryClient.invalidateQueries({ queryKey: ['admin', 'users'], exact: true })
    },
  })

  if (!isNew && user.isLoading) return <Loading />
  const fields = save.error instanceof ApiError ? save.error.fields : {}

  function submit(e: FormEvent) {
    e.preventDefault()
    save.mutate()
  }

  return (
    <>
      <PageHeader title={isNew ? 'Tambah user' : `Ubah user ${user.data?.username ?? ''}`} />
      <form onSubmit={submit} className="card max-w-lg space-y-4">
        <ErrorMessage error={user.error ?? save.error ?? setActive.error} />
        <label className="block text-sm">
          Username
          <input
            className="input mt-1"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            disabled={!isNew}
            required
          />
          <FieldError message={fields.username} />
        </label>
        <label className="block text-sm">
          Password {!isNew && <span className="text-slate-500">(kosongkan bila tidak diganti)</span>}
          <input
            className="input mt-1"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required={isNew}
            autoComplete="new-password"
          />
          <FieldError message={fields.password} />
        </label>
        <label className="flex min-h-[44px] items-center gap-2 text-sm">
          <input type="checkbox" checked={isAdmin} onChange={(e) => setIsAdmin(e.target.checked)} />
          Platform Admin
        </label>
        <FieldError message={fields.is_platform_admin} />
        <div className="flex flex-wrap gap-2">
          <button className="btn-primary" disabled={save.isPending}>
            Simpan
          </button>
          {user.data && (
            <button
              type="button"
              className={user.data.is_active ? 'btn-danger' : 'btn-secondary'}
              onClick={() => setActive.mutate(!user.data.is_active)}
            >
              {user.data.is_active ? 'Nonaktifkan' : 'Aktifkan'}
            </button>
          )}
        </div>
      </form>
    </>
  )
}
