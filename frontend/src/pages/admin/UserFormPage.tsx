import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '../../api/client'
import type { User } from '../../api/types'
import Badge from '../../components/Badge'
import Breadcrumbs from '../../components/Breadcrumbs'
import ErrorMessage from '../../components/ErrorMessage'
import Field from '../../components/Field'
import FieldError from '../../components/FieldError'
import FormFooter from '../../components/FormFooter'
import FormSection from '../../components/FormSection'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import { useSession } from '../../hooks/useSession'

// UserFormPage dipakai untuk /admin/users/new (buat) dan /admin/users/:id (ubah).
export default function UserFormPage() {
  const { id } = useParams()
  const isNew = id === undefined
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { data: session } = useSession()
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
    mutationFn: (active: boolean) => api<User>('POST', `/admin/users/${id}/${active ? 'activate' : 'deactivate'}`),
    onSuccess: (u) => {
      queryClient.setQueryData(['admin', 'users', id], u)
      queryClient.invalidateQueries({ queryKey: ['admin', 'users'], exact: true })
    },
  })

  if (!isNew && user.isLoading) return <Loading />
  if (!isNew && !user.data) return <ErrorMessage error={user.error} />

  const isSelf = !isNew && user.data?.id === session?.user.id
  const fields = save.error instanceof ApiError ? save.error.fields : {}
  const otherError = [save.error, setActive.error].find((e) => e && !(e instanceof ApiError && e.status === 422))

  function submit(e: FormEvent) {
    e.preventDefault()
    save.mutate()
  }

  return (
    <>
      <Breadcrumbs
        items={[{ label: 'Pengguna', to: '/admin/users' }, { label: isNew ? 'Baru' : (user.data?.username ?? '') }]}
      />
      <PageHeader
        title={isNew ? 'Tambah pengguna' : `Ubah ${user.data?.username ?? ''}`}
        titleExtra={
          user.data && (
            <Badge tone={user.data.is_active ? 'green' : 'neutral'} dot>
              {user.data.is_active ? 'Aktif' : 'Nonaktif'}
            </Badge>
          )
        }
        subtitle={
          isNew
            ? 'Buat akun baru. Akses ke database diatur dengan memasukkan pengguna ke group.'
            : 'Username tidak bisa diubah. Isi password hanya bila ingin menggantinya.'
        }
      />

      <div className="max-w-[860px] space-y-4">
        <ErrorMessage error={otherError} />

        <form onSubmit={submit} className="card overflow-hidden p-0" noValidate autoComplete="off">
          <FormSection icon="users" tone="green" title="Akun" desc="Nama untuk masuk dan password awalnya.">
            <Field label="Username" error={fields.username} hint={isNew ? undefined : 'Tidak bisa diubah.'}>
              <input
                className="input"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                readOnly={!isNew}
                autoCapitalize="none"
                spellCheck={false}
                maxLength={32}
                required
              />
            </Field>
            <Field
              label="Password"
              error={fields.password}
              hint={isNew ? undefined : 'Kosongkan jika password tidak ingin diubah.'}
            >
              <input
                className="input"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                maxLength={72}
                required={isNew}
              />
            </Field>
          </FormSection>

          <FormSection
            icon="shield"
            tone="butter"
            title="Peran"
            desc="Platform Admin bisa mengelola pengguna, group, dan koneksi."
            columns={1}
          >
            <div>
              <label className="check">
                <input
                  type="checkbox"
                  checked={isAdmin}
                  onChange={(e) => setIsAdmin(e.target.checked)}
                  disabled={isSelf}
                />
                Platform Admin
              </label>
              {/* Backend menolak admin menurunkan perannya sendiri, jadi pilihan ini dikunci. */}
              {isSelf && <span className="hint">Kamu tidak bisa menurunkan peran akunmu sendiri.</span>}
              <FieldError message={fields.is_platform_admin} />
            </div>
          </FormSection>

          <FormFooter>
            <button type="submit" className="btn-primary" disabled={save.isPending}>
              <Icon name="check" />
              {save.isPending ? 'Menyimpan…' : 'Simpan'}
            </button>
            <Link to="/admin/users" className="btn-secondary">
              Batal
            </Link>
            {user.data && !isSelf && (
              <button
                type="button"
                className={(user.data.is_active ? 'btn-danger' : 'btn-dark') + ' sm:ml-auto'}
                disabled={setActive.isPending}
                onClick={() => setActive.mutate(!user.data.is_active)}
              >
                {user.data.is_active ? 'Nonaktifkan' : 'Aktifkan'}
              </button>
            )}
          </FormFooter>
        </form>
      </div>
    </>
  )
}
