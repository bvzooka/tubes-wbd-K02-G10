import { useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '../../api/client'
import type { DatabaseItem, SchemaDiffCreated, SchemaDiffPreview, SchemaDiffSources } from '../../api/types'
import Badge from '../../components/Badge'
import Breadcrumbs from '../../components/Breadcrumbs'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Field from '../../components/Field'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import { databasePath } from '../databases/paths'
import { editMigrationPath, migrationsKey, migrationsPath } from '../migrations/paths'
import { changeLabel } from './labels'

// pesan per field dari backend lebih jelas daripada pesan umumnya
function problem(error: unknown) {
  if (error instanceof ApiError && error.status === 422) {
    return new ApiError(error.status, error.code, Object.values(error.fields)[0] ?? error.message, {}, error.requestId)
  }
  return error
}

export default function SchemaDiffPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [sourceID, setSourceID] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())

  const sources = useQuery({
    queryKey: ['schema-diff', id, 'sources'],
    queryFn: () => api<SchemaDiffSources>('GET', `/databases/${id}/schema-diff`),
  })
  const databases = useQuery({ queryKey: ['databases'], queryFn: () => api<DatabaseItem[]>('GET', '/databases') })
  const dbName = databases.data?.find((d) => String(d.id) === id)?.name ?? `Database ${id}`

  // hasil perbandingan tidak disimpan di cache karena schema bisa berubah kapan saja
  const preview = useMutation({
    mutationFn: (source: number) =>
      api<SchemaDiffPreview>('POST', `/databases/${id}/schema-diff/preview`, { source_id: source }),
    // yang bisa menghapus data harus dicentang sendiri oleh user
    onSuccess: (result) => setSelected(new Set(result.changes.filter((c) => !c.destructive).map((c) => c.id))),
  })
  const create = useMutation({
    mutationFn: () =>
      api<SchemaDiffCreated>('POST', `/databases/${id}/schema-diff`, {
        // sumber diambil dari perbandingan yang tampil, bukan dari pilihan yang mungkin sudah diganti
        source_id: preview.variables,
        changes: [...selected],
      }),
    onSuccess: (draft) => {
      queryClient.invalidateQueries({ queryKey: migrationsKey(id) })
      // langsung ke editor supaya SQL-nya ditinjau dan SQL down dilengkapi
      navigate(editMigrationPath(id, draft.version))
    },
  })

  if (sources.isLoading) return <Loading />
  if (!sources.data) return <ErrorMessage error={sources.error} />
  const options = sources.data.sources
  const result = preview.data
  const destructive = result?.changes.filter((c) => c.destructive).length ?? 0

  function pickSource(value: string) {
    setSourceID(value)
    // daftar lama dibuang supaya tidak dikira hasil dari sumber yang baru
    preview.reset()
    create.reset()
  }

  function compare(e: FormEvent) {
    e.preventDefault()
    create.reset()
    preview.mutate(Number(sourceID))
  }

  function toggle(changeID: string) {
    setSelected((current) => {
      const next = new Set(current)
      if (!next.delete(changeID)) next.add(changeID)
      return next
    })
  }

  return (
    <>
      <Breadcrumbs
        items={[
          { label: 'Database', to: '/databases' },
          { label: dbName, to: databasePath(id) },
          { label: 'Migration', to: migrationsPath(id) },
          { label: 'Schema diff' },
        ]}
      />
      <PageHeader
        eyebrow={{ icon: 'layers', label: dbName }}
        title="Schema diff"
        subtitle={`Membandingkan struktur database sumber dengan ${dbName}. Hasilnya draft migration yang membuat ${dbName} mirip sumber, datanya tidak ikut disalin.`}
      />

      {options.length === 0 ? (
        <EmptyState icon="db" message="Tidak ada database PostgreSQL lain yang bisa kamu baca untuk dijadikan sumber." />
      ) : (
        <div className="space-y-4">
          <form onSubmit={compare} className="card flex flex-wrap items-end gap-3">
            <div className="min-w-[220px] max-w-[420px] flex-1">
              <Field label="Database sumber">
                <select className="input" value={sourceID} onChange={(e) => pickSource(e.target.value)}>
                  <option value="">Pilih database</option>
                  {options.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </select>
              </Field>
            </div>
            <button type="submit" className="btn-primary" disabled={sourceID === '' || preview.isPending}>
              {preview.isPending ? 'Membandingkan…' : 'Bandingkan'}
            </button>
          </form>

          <ErrorMessage error={problem(preview.error ?? create.error)} />

          {result && result.changes.length === 0 && (
            <p role="status" className="alert alert-success">
              <span className="alert-mark" aria-hidden="true">
                ✓
              </span>
              Tidak ada perbedaan struktur antara {result.source_name} dan {result.target_name}.
            </p>
          )}

          {result && result.changes.length > 0 && (
            <>
              <h2 className="mb-0 text-[1.2rem]">
                {result.changes.length} perubahan dari {result.source_name}
              </h2>
              {destructive > 0 && (
                <p className="alert alert-warning">
                  <span className="alert-mark" aria-hidden="true">
                    !
                  </span>
                  {destructive} perubahan bisa menghapus data sehingga tidak dicentang. Centang hanya kalau memang
                  diinginkan.
                </p>
              )}
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Pilih</th>
                      <th>Perubahan</th>
                      <th>Objek</th>
                      <th>SQL</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.changes.map((c) => (
                      <tr key={c.id}>
                        <td>
                          <label className="check">
                            <input
                              type="checkbox"
                              aria-label={`${changeLabel(c.kind)} ${c.object}`}
                              checked={selected.has(c.id)}
                              onChange={() => toggle(c.id)}
                            />
                          </label>
                        </td>
                        <td>
                          <span className="whitespace-nowrap font-bold">{changeLabel(c.kind)}</span>
                          {c.destructive && (
                            <span className="mt-1 block">
                              <Badge tone="red">Menghapus data</Badge>
                            </span>
                          )}
                        </td>
                        <td>
                          <code>{c.object}</code>
                        </td>
                        <td>
                          {/* SQL panjang menggulir di dalam selnya supaya tabel tidak ikut melebar */}
                          <pre className="m-0 max-h-48 max-w-[460px] overflow-auto rounded-md bg-ink p-3 font-mono text-xs text-paper-soft">
                            {c.sql}
                          </pre>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="flex flex-wrap items-center gap-3">
                <button
                  type="button"
                  className="btn-primary"
                  disabled={selected.size === 0 || create.isPending}
                  onClick={() => create.mutate()}
                >
                  <Icon name="plus" />
                  {create.isPending ? 'Membuat…' : `Buat draft dari ${selected.size} perubahan`}
                </button>
                <span className="text-[0.86rem] text-muted">
                  Perubahan yang saling bergantung perlu dicentang bersama.
                </span>
              </div>
            </>
          )}
        </div>
      )}
    </>
  )
}
