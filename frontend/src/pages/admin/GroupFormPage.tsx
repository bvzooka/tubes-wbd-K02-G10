import { useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '../../api/client'
import type { Group } from '../../api/types'
import Breadcrumbs from '../../components/Breadcrumbs'
import ErrorMessage from '../../components/ErrorMessage'
import Field from '../../components/Field'
import FormFooter from '../../components/FormFooter'
import FormSection from '../../components/FormSection'
import Icon from '../../components/Icon'
import PageHeader from '../../components/PageHeader'

export default function GroupFormPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')

  const create = useMutation({
    mutationFn: () => api<Group>('POST', '/admin/groups', { name, description }),
    onSuccess: (g) => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'groups'] })
      navigate(`/admin/groups/${g.id}`)
    },
  })
  const fields = create.error instanceof ApiError ? create.error.fields : {}
  const otherError = create.error instanceof ApiError && create.error.status === 422 ? null : create.error

  function submit(e: FormEvent) {
    e.preventDefault()
    create.mutate()
  }

  return (
    <>
      <Breadcrumbs items={[{ label: 'Group', to: '/admin/groups' }, { label: 'Baru' }]} />
      <PageHeader
        title="Tambah group"
        subtitle="Setelah group dibuat, kamu bisa menambah anggota, memilih leader, dan memberi akses database."
      />
      <div className="max-w-[860px] space-y-4">
        <ErrorMessage error={otherError} />
        <form onSubmit={submit} className="card overflow-hidden p-0" noValidate>
          <FormSection icon="grid" tone="green" title="Identitas group" columns={1}>
            <Field label="Nama group" error={fields.name}>
              <input className="input" value={name} onChange={(e) => setName(e.target.value)} maxLength={64} required />
            </Field>
            <Field label="Deskripsi (opsional)" error={fields.description}>
              <textarea
                className="input"
                rows={3}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                maxLength={500}
              />
            </Field>
          </FormSection>
          <FormFooter>
            <button type="submit" className="btn-primary" disabled={create.isPending}>
              <Icon name="check" />
              {create.isPending ? 'Menyimpan…' : 'Buat group'}
            </button>
            <Link to="/admin/groups" className="btn-secondary">
              Batal
            </Link>
          </FormFooter>
        </form>
      </div>
    </>
  )
}
