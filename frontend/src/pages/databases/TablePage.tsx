import { useRef, useState, type KeyboardEvent } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { Constraint, ConstraintType, TableDetail } from '../../api/types'
import Badge from '../../components/Badge'
import Breadcrumbs from '../../components/Breadcrumbs'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import StatCard from '../../components/StatCard'
import { databasePath, formatRows, tableDataPath, tablePath } from './paths'

const constraintLabel: Record<ConstraintType, string> = {
  primary_key: 'Primary key',
  foreign_key: 'Foreign key',
  unique: 'Unique',
  check: 'Check',
}

// constraintDetail merangkum isi constraint sesuai jenisnya.
function constraintDetail(c: Constraint): string {
  if (c.type === 'check') return c.check_expr ?? ''
  if (c.type === 'foreign_key') {
    const ref = `${c.ref_schema}.${c.ref_table}(${(c.ref_columns ?? []).join(', ')})`
    const rules = [c.on_update && `ON UPDATE ${c.on_update}`, c.on_delete && `ON DELETE ${c.on_delete}`]
    return ['→ ' + ref, ...rules.filter(Boolean)].join(' ')
  }
  return ''
}

type Tab = 'columns' | 'constraints' | 'indexes'
const tabs: { id: Tab; label: string }[] = [
  { id: 'columns', label: 'Kolom' },
  { id: 'constraints', label: 'Constraint' },
  { id: 'indexes', label: 'Index' },
]

export default function TablePage() {
  const { id = '', schema = '', table = '' } = useParams()
  const [tab, setTab] = useState<Tab>('columns')
  const tabRefs = useRef<Record<Tab, HTMLButtonElement | null>>({ columns: null, constraints: null, indexes: null })
  const { data: t, isLoading, error } = useQuery({
    queryKey: ['databases', id, 'tables', schema, table],
    queryFn: () => api<TableDetail>('GET', tablePath(id, schema, table)),
  })

  if (isLoading) return <Loading />
  if (!t) return <ErrorMessage error={error} />

  const counts: Record<Tab, number> = {
    columns: t.columns.length,
    constraints: t.constraints.length,
    indexes: t.indexes.length,
  }

  // Panah kiri/kanan memindah tab, sesuai pola ARIA tablist.
  function onTabKey(e: KeyboardEvent) {
    if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return
    const i = tabs.findIndex((x) => x.id === tab)
    const next = tabs[(i + (e.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length].id
    setTab(next)
    tabRefs.current[next]?.focus()
  }

  return (
    <>
      <Breadcrumbs
        items={[
          { label: 'Database', to: '/databases' },
          { label: t.database.name, to: databasePath(t.database.id) },
          { label: `${t.schema}.${t.name}` },
        ]}
      />

      <section className="dark-card">
        <div className="mb-5 flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0">
            <span className="mb-2.5 inline-flex items-center gap-[0.45rem] rounded-full border-[1.5px] border-white/30 px-[0.85rem] py-[0.3rem] text-[0.8rem] font-bold text-lime">
              <Icon name="table" />
              {t.kind === 'view' ? 'View' : 'Tabel'} di schema {t.schema}
            </span>
            <h1 className="m-0 break-all text-[clamp(2rem,4.5vw,3.2rem)] font-light tracking-[-0.04em] text-white">
              {t.name}
            </h1>
          </div>
          <Link to={tableDataPath(t.database.id, t.schema, t.name)} className="btn-lime">
            <Icon name="grid" />
            Lihat data
          </Link>
        </div>
        <div className="mb-3.5 flex flex-wrap gap-1.5" aria-label="Ringkasan kolom">
          {t.columns.map((c) => (
            <span
              key={c.name}
              title={`${c.name}: ${c.data_type}`}
              className={
                'col-pill ' + (c.nullable ? 'nullable' : 'notnull') + (c.default !== null ? ' hasdef' : '')
              }
            >
              {c.name}
            </span>
          ))}
        </div>
        <div className="flex flex-wrap gap-x-5 gap-y-2 text-[0.8rem] text-white/75">
          <span className="inline-flex items-center gap-1.5">
            <i className="h-3 w-[22px] rounded-full bg-lime" />
            NOT NULL
          </span>
          <span className="inline-flex items-center gap-1.5">
            <i className="h-3 w-[22px] rounded-full border-[1.5px] border-white/55 bg-white/[.12]" />
            boleh NULL
          </span>
          <span className="inline-flex items-center gap-1.5">
            <i className="h-[7px] w-[7px] rounded-full bg-white" />
            punya default
          </span>
        </div>
      </section>

      <div className="mb-7 grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-4">
        <StatCard icon="columns" label="Kolom" value={counts.columns} />
        <StatCard icon="link" label="Constraint" value={counts.constraints} />
        <StatCard icon="bolt" label="Index" value={counts.indexes} />
        <StatCard icon="hash" label="Estimasi baris" value={formatRows(t.estimated_rows)} accent />
      </div>

      <div role="tablist" aria-label="Struktur tabel" className="tab-list" onKeyDown={onTabKey}>
        {tabs.map((x) => (
          <button
            key={x.id}
            ref={(el) => (tabRefs.current[x.id] = el)}
            type="button"
            role="tab"
            id={`tab-${x.id}`}
            aria-selected={tab === x.id}
            aria-controls={`panel-${x.id}`}
            tabIndex={tab === x.id ? 0 : -1}
            className="tab"
            onClick={() => setTab(x.id)}
          >
            {x.label} <span className="tab-count">{counts[x.id]}</span>
          </button>
        ))}
      </div>

      <section role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`} className="table-wrap">
        {tab === 'columns' &&
          (t.columns.length === 0 ? (
            <EmptyState plain icon="columns" title="Tidak ada kolom" message="Relasi ini tidak punya kolom." />
          ) : (
            <table className="table">
              <thead>
                <tr>
                  <th className="text-right">#</th>
                  <th>Nama</th>
                  <th>Tipe</th>
                  <th>Nullable</th>
                  <th>Default</th>
                </tr>
              </thead>
              <tbody>
                {t.columns.map((c) => (
                  <tr key={c.name}>
                    <td className="text-right tabular-nums text-muted">{c.position}</td>
                    <td className="whitespace-nowrap font-bold">{c.name}</td>
                    <td className="whitespace-nowrap">
                      <code>{c.data_type}</code>
                    </td>
                    <td>{c.nullable ? <Badge>boleh NULL</Badge> : <Badge tone="lime">NOT NULL</Badge>}</td>
                    <td className="min-w-[10rem] break-words">
                      {c.default === null ? <span className="text-muted">–</span> : <code>{c.default}</code>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ))}

        {tab === 'constraints' &&
          (t.constraints.length === 0 ? (
            <EmptyState plain icon="link" title="Tidak ada constraint" message="Tabel ini tidak punya constraint." />
          ) : (
            <table className="table">
              <thead>
                <tr>
                  <th>Nama</th>
                  <th>Jenis</th>
                  <th>Kolom</th>
                  <th>Detail</th>
                </tr>
              </thead>
              <tbody>
                {t.constraints.map((c) => (
                  <tr key={c.name}>
                    <td className="whitespace-nowrap font-bold">{c.name}</td>
                    <td>
                      <Badge tone="outline">{constraintLabel[c.type] ?? c.type}</Badge>
                    </td>
                    <td className="whitespace-nowrap">
                      <code>{c.columns.join(', ')}</code>
                    </td>
                    <td className="min-w-[16rem] break-words font-mono text-[0.85rem]">{constraintDetail(c)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ))}

        {tab === 'indexes' &&
          (t.indexes.length === 0 ? (
            <EmptyState plain icon="bolt" title="Tidak ada index" message="Tabel ini tidak punya index." />
          ) : (
            <table className="table">
              <thead>
                <tr>
                  <th>Nama</th>
                  <th>Metode</th>
                  <th>Kolom</th>
                  <th>Sifat</th>
                  <th>Definisi</th>
                </tr>
              </thead>
              <tbody>
                {t.indexes.map((ix) => (
                  <tr key={ix.name}>
                    <td className="whitespace-nowrap font-bold">{ix.name}</td>
                    <td>{ix.method}</td>
                    <td className="whitespace-nowrap">
                      <code>{ix.columns.join(', ')}</code>
                    </td>
                    <td>
                      {ix.primary ? (
                        <Badge tone="ink">primary</Badge>
                      ) : ix.unique ? (
                        <Badge tone="butter">unique</Badge>
                      ) : (
                        <span className="text-muted">–</span>
                      )}
                    </td>
                    <td className="min-w-[16rem] break-words">
                      <code>{ix.definition}</code>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          ))}
      </section>
    </>
  )
}
