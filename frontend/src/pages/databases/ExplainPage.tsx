import { useState, type FormEvent } from 'react'
import { useParams } from 'react-router-dom'
import { useMutation, useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { ExplainResult, SchemaSummary } from '../../api/types'
import Breadcrumbs from '../../components/Breadcrumbs'
import ErrorMessage from '../../components/ErrorMessage'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import ExplainOutput from './ExplainOutput'
import { databasePath, dbmsLabel } from './paths'

export default function ExplainPage() {
  const { id = '' } = useParams()
  const [sql, setSql] = useState('')
  const [analyze, setAnalyze] = useState(false)

  // cuma buat ambil nama database dan hak edit
  const db = useQuery({
    queryKey: ['databases', id, 'schema'],
    queryFn: () => api<SchemaSummary>('GET', `/databases/${id}/schema`),
  })
  const explain = useMutation({
    mutationFn: () => api<ExplainResult>('POST', `/databases/${id}/explain`, { sql, analyze }),
  })

  if (db.isLoading) return <Loading />
  if (!db.data) return <ErrorMessage error={db.error} />
  const database = db.data.database

  function submit(e: FormEvent) {
    e.preventDefault()
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
          <button type="submit" className="btn-primary" disabled={explain.isPending || !sql.trim()}>
            <Icon name="bolt" />
            {explain.isPending ? 'Menganalisis…' : 'Analisis plan'}
          </button>
        </form>
      )}

      <ErrorMessage error={explain.error} />
      {explain.data && <ExplainOutput result={explain.data} />}
    </>
  )
}
