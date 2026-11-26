import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { SchemaSummary } from '../../api/types'
import Badge from '../../components/Badge'
import Breadcrumbs from '../../components/Breadcrumbs'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import StatCard from '../../components/StatCard'
import { migrationsPath } from '../migrations/paths'
import { accessLabel, dbmsLabel, explainPath, formatRows, tableDataPath, tablePath } from './paths'

export default function SchemaPage() {
  const { id = '' } = useParams()
  const [query, setQuery] = useState('')
  const finder = useRef<HTMLInputElement>(null)
  const { data, isLoading, error } = useQuery({
    queryKey: ['databases', id, 'schema'],
    queryFn: () => api<SchemaSummary>('GET', `/databases/${id}/schema`),
  })

  // Pintasan dari M1: "/" memfokuskan kolom cari, Esc mengosongkannya.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const tag = (e.target as HTMLElement).tagName
      if (e.key === '/' && !['INPUT', 'TEXTAREA', 'SELECT'].includes(tag)) {
        e.preventDefault()
        finder.current?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  if (isLoading) return <Loading />
  if (!data) return <ErrorMessage error={error} />
  const db = data.database

  const q = query.trim().toLowerCase()
  const schemas = data.schemas
    .map((s) => ({ ...s, tables: s.tables.filter((t) => !q || t.name.toLowerCase().includes(q)) }))
    .filter((s) => s.tables.length > 0)

  return (
    <>
      <Breadcrumbs items={[{ label: 'Database', to: '/databases' }, { label: db.name }]} />
      <PageHeader
        eyebrow={{ icon: 'db', label: dbmsLabel[db.dbms] ?? db.dbms }}
        title={db.name}
        titleExtra={<Badge tone={db.can_edit ? 'lime' : 'butter'}>{accessLabel[db.access_level]}</Badge>}
        subtitle="Pilih tabel atau view untuk melihat kolom, constraint, index, dan isi datanya."
        action={
          <>
            {/* View Only juga boleh melihat daftar migration */}
            <Link to={migrationsPath(db.id)} className="btn-secondary">
              <Icon name="history" />
              Migration
            </Link>
            {db.can_edit && (
              <Link to={explainPath(db.id)} className="btn-secondary">
                <Icon name="bolt" />
                Analisis query plan
              </Link>
            )}
          </>
        }
        aside={<StatCard icon="layers" label="Schema" value={data.schemas.length} />}
      />

      {data.schemas.length === 0 ? (
        <EmptyState icon="layers" title="Schema kosong" message="Database ini belum punya tabel atau view." />
      ) : (
        <>
          <div className="relative mb-5 max-w-[420px]">
            <Icon
              name="search"
              className="pointer-events-none absolute left-4 top-1/2 h-[1.15em] w-[1.15em] -translate-y-1/2 text-muted"
            />
            <input
              ref={finder}
              type="search"
              className="input rounded-full bg-white/85 pl-11 pr-12"
              placeholder="Cari tabel atau view…"
              aria-label="Cari tabel atau view"
              autoComplete="off"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Escape') {
                  setQuery('')
                  e.currentTarget.blur()
                }
              }}
            />
            <kbd className="pointer-events-none absolute right-3.5 top-1/2 -translate-y-1/2 rounded-[6px] border border-line-strong bg-paper px-[0.45rem] py-px font-mono text-[0.75rem] font-semibold text-muted max-sm:hidden">
              /
            </kbd>
          </div>

          {schemas.length === 0 && (
            <p className="mb-4 p-4 text-center text-muted">Tidak ada tabel atau view yang cocok dengan “{query}”.</p>
          )}

          {schemas.map((schema, i) => (
            <section key={schema.name} className="surface" aria-labelledby={`schema-${i}`}>
              <div className="mx-1.5 mb-4 flex items-center justify-between gap-4">
                <h2 id={`schema-${i}`} className="m-0 flex min-w-0 items-center gap-2.5 text-[1.2rem]">
                  <span className="grid h-[1.5em] w-[1.5em] flex-none place-items-center rounded-full bg-ink text-lime">
                    <Icon name="layers" className="h-[0.9em] w-[0.9em]" />
                  </span>
                  <span className="min-w-0 break-all">
                    Schema <code>{schema.name}</code>
                  </span>
                </h2>
                <span className="flex-none rounded-full border border-line bg-paper px-3 py-1 text-[0.82rem] font-bold text-muted">
                  {schema.tables.length} objek
                </span>
              </div>
              <ul className="m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(250px,1fr))] gap-4 p-0">
                {schema.tables.map((t) => (
                  <li key={t.name} className="tile">
                    <div className="flex items-center justify-between gap-3">
                      <span className="tile-icon">
                        <Icon name="table" className="h-[22px] w-[22px]" />
                      </span>
                      <Badge tone="outline">{t.kind === 'view' ? 'View' : 'Tabel'}</Badge>
                    </div>
                    <h3 className="m-0 break-all text-[1.2rem] font-bold tracking-[-0.02em] text-inherit">
                      <Link to={tablePath(db.id, t.schema, t.name)} className="tile-link">
                        {t.name}
                      </Link>
                    </h3>
                    <div>
                      <span className="tile-big">{formatRows(t.estimated_rows)}</span>
                      <span className="tile-cap">
                        {t.estimated_rows === null ? 'estimasi tidak diketahui' : 'estimasi baris'}
                      </span>
                    </div>
                    <div className="mt-auto flex items-center justify-between gap-3">
                      <Link to={tableDataPath(db.id, t.schema, t.name)} className="tile-sub">
                        <Icon name="grid" />
                        Lihat data
                      </Link>
                      <span className="tile-go" aria-hidden="true">
                        <Icon name="arrow" className="h-5 w-5" />
                      </span>
                    </div>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </>
      )}
    </>
  )
}
