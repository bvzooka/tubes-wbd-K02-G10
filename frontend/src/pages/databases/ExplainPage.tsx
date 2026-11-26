import { useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError } from '../../api/client'
import type { ExplainResult, Migration, SchemaSummary } from '../../api/types'
import Breadcrumbs from '../../components/Breadcrumbs'
import ErrorMessage from '../../components/ErrorMessage'
import Field from '../../components/Field'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import { editMigrationPath, migrationsKey } from '../migrations/paths'
import ExplainOutput from './ExplainOutput'
import { databasePath, dbmsLabel } from './paths'

export default function ExplainPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [sql, setSql] = useState('')
  const [analyze, setAnalyze] = useState(false)
  const [name, setName] = useState('')

  // cuma buat ambil nama database dan hak edit
  const db = useQuery({
    queryKey: ['databases', id, 'schema'],
    queryFn: () => api<SchemaSummary>('GET', `/databases/${id}/schema`),
  })
  const explain = useMutation({
    mutationFn: () => api<ExplainResult>('POST', `/databases/${id}/explain`, { sql, analyze }),
  })
  // hanya menyimpan draft, menjalankannya tetap lewat freeze dan persetujuan
  const save = useMutation({
    mutationFn: () => api<Migration>('POST', `/databases/${id}/migrations`, { name, sql_up: sql }),
    onSuccess: (draft) => {
      queryClient.invalidateQueries({ queryKey: migrationsKey(id) })
      navigate(editMigrationPath(id, draft.version))
    },
  })

  if (db.isLoading) return <Loading />
  if (!db.data) return <ErrorMessage error={db.error} />
  const database = db.data.database
  const invalid = save.error instanceof ApiError && save.error.status === 422 ? save.error : null
  // error nama tampil di bawah isiannya, sisanya jadi pesan di bawah form
  let saveError: unknown = save.error
  if (invalid?.fields.sql_up) saveError = new Error(invalid.fields.sql_up)
  else if (invalid?.fields.name) saveError = null

  function submit(e: FormEvent) {
    e.preventDefault()
    // pesan gagal simpan sebelumnya tidak ikut tampil di bawah hasil analisis
    save.reset()
    explain.mutate()
  }

  return (
    <>
      <Breadcrumbs
        items={[
          { label: 'Database', to: '/databases' },
          { label: database.name, to: databasePath(database.id) },
          { label: 'Query plan' },
        ]}
      />
      <PageHeader
        eyebrow={{ icon: 'bolt', label: dbmsLabel[database.dbms] ?? database.dbms }}
        title="Query plan"
        subtitle="Lihat bagaimana database menjalankan satu statement DML: jenis scan, index, join, dan perkiraan biaya."
      />

      {!database.can_edit ? (
        <ErrorMessage error={new Error('Analisis query plan hanya untuk pengguna dengan akses View + Edit.')} />
      ) : (
        <form onSubmit={submit} className="card mb-5 space-y-4">
          <label className="block">
            <span className="mb-[0.35rem] block">SQL</span>
            <textarea
              className="input min-h-[180px] py-3 font-mono text-[0.88rem]"
              value={sql}
              onChange={(e) => setSql(e.target.value)}
              placeholder="SELECT * FROM orders WHERE customer_id = 42"
              spellCheck={false}
              required
            />
          </label>
          <div>
            <label className="check">
              <input type="checkbox" checked={analyze} onChange={(e) => setAnalyze(e.target.checked)} />
              EXPLAIN ANALYZE
            </label>
            <span className="hint">
              ANALYZE benar-benar menjalankan statement untuk mengukur waktu nyata, lalu selalu di-rollback.
            </span>
          </div>
          <Field
            label="Nama migration"
            error={invalid?.fields.name}
            hint="Hanya diperlukan kalau statement ini mau disimpan sebagai migration."
          >
            <input className="input" value={name} maxLength={120} onChange={(e) => setName(e.target.value)} />
          </Field>
          <div className="flex flex-wrap gap-2.5">
            <button type="submit" className="btn-primary" disabled={explain.isPending || !sql.trim()}>
              <Icon name="bolt" />
              {explain.isPending ? 'Menganalisis…' : 'Analisis plan'}
            </button>
            <button
              type="button"
              className="btn-secondary"
              disabled={save.isPending || !sql.trim()}
              onClick={() => save.mutate()}
            >
              <Icon name="plus" />
              {save.isPending ? 'Menyimpan…' : 'Simpan sebagai draft'}
            </button>
          </div>
        </form>
      )}

      <ErrorMessage error={saveError} />
      <ErrorMessage error={explain.error} />
      {explain.data && <ExplainOutput result={explain.data} />}
    </>
  )
}
