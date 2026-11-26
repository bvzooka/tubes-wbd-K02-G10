import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { DatabaseItem, MigrationList } from '../../api/types'
import Badge from '../../components/Badge'
import Breadcrumbs from '../../components/Breadcrumbs'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import { databasePath } from '../databases/paths'
import { modeLabel, StateBadge } from './labels'
import { editMigrationPath, migrationPath, migrationsKey, newMigrationPath } from './paths'

export default function MigrationsPage() {
  const { id = '' } = useParams()
  const list = useQuery({
    queryKey: migrationsKey(id),
    queryFn: () => api<MigrationList>('GET', `/databases/${id}/migrations`),
  })
  // nama database diambil dari daftar database, cache-nya sama dengan halaman Database
  const databases = useQuery({ queryKey: ['databases'], queryFn: () => api<DatabaseItem[]>('GET', '/databases') })
  const dbName = databases.data?.find((d) => String(d.id) === id)?.name ?? `Database ${id}`

  if (list.isLoading) return <Loading />
  if (!list.data) return <ErrorMessage error={list.error} />
  const { items, can_edit } = list.data

  // tombol hanya disembunyikan, penolakan sebenarnya dilakukan backend
  const addButton = can_edit ? (
    <Link to={newMigrationPath(id)} className="btn-primary">
      <Icon name="plus" />
      Buat migration
    </Link>
  ) : undefined

  return (
    <>
      <Breadcrumbs
        items={[{ label: 'Database', to: '/databases' }, { label: dbName, to: databasePath(id) }, { label: 'Migration' }]}
      />
      <PageHeader
        eyebrow={{ icon: 'history', label: dbName }}
        title="Migration"
        subtitle="Perubahan database berurutan menurut versi. Versi kecil harus selesai sebelum versi di atasnya."
        action={addButton}
      />

      {items.length === 0 ? (
        <EmptyState icon="history" title="Belum ada migration" message="Buat draft pertama untuk database ini." action={addButton} />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>Versi</th>
                <th>Nama</th>
                <th>Mode</th>
                <th>Status</th>
                <th>Pembuat</th>
                <th>Diubah</th>
                <th>Aksi</th>
              </tr>
            </thead>
            <tbody>
              {items.map((m) => (
                <tr key={m.id}>
                  <td className="font-bold tabular-nums">v{m.version}</td>
                  <td>
                    <Link to={migrationPath(id, m.version)} className="font-bold">
                      {m.name}
                    </Link>
                    {m.irreversible && <span className="mt-1 block text-[0.78rem] text-muted">irreversible</span>}
                  </td>
                  <td>
                    <Badge tone="outline">{modeLabel[m.mode]}</Badge>
                  </td>
                  <td>
                    <StateBadge state={m.state} />
                  </td>
                  <td className="whitespace-nowrap">{m.created_by}</td>
                  <td className="whitespace-nowrap">{new Date(m.updated_at).toLocaleString('id-ID')}</td>
                  <td>
                    {m.state === 'draft' && can_edit && (
                      <Link to={editMigrationPath(id, m.version)} className="btn-link" aria-label={`Ubah versi ${m.version}`}>
                        <Icon name="edit" />
                        Ubah
                      </Link>
                    )}
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
