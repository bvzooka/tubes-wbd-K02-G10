import { useEffect, useRef } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { TableData } from '../../api/types'
import Breadcrumbs from '../../components/Breadcrumbs'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import Pager from '../../components/Pager'
import { databasePath, tableDataPath, tablePath } from './paths'

// readPage membaca ?page= dari URL. Nilai kosong atau rusak menjadi 1,
function readPage(params: URLSearchParams): number {
  const n = Number(params.get('page'))
  return Number.isInteger(n) && n >= 1 ? n : 1
}

export default function TableDataPage() {
  const { id = '', schema = '', table = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const page = readPage(params)

  // keepPreviousData membuat tabel lama tetap tampil saat pindah halaman, bukan berkedip ke "Memuat".
  const { data, isLoading, isFetching, isPlaceholderData, error } = useQuery({
    queryKey: ['databases', id, 'tables', schema, table, 'data', page],
    queryFn: () => api<TableData>('GET', `${tableDataPath(id, schema, table)}?page=${page}`),
    placeholderData: keepPreviousData,
  })

  // Halaman baru ditampilkan dari baris pertamanya, bukan dari posisi scroll halaman sebelumnya.
  // Dijalankan saat data halaman baru tiba, supaya data lama tidak ikut melompat selama memuat.
  const tableRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (tableRef.current) tableRef.current.scrollTop = 0
  }, [data?.page])

  // Halaman disimpan di query string supaya bisa di-refresh dan tombol Back browser bekerja.
  function goTo(next: number) {
    setParams(next > 1 ? { page: String(next) } : {})
  }

  if (isLoading) return <Loading />
  if (!data) return <ErrorMessage error={error} />
  const structure = tablePath(data.database.id, data.schema, data.name)

  return (
    <>
      <Breadcrumbs
        items={[
          { label: 'Database', to: '/databases' },
          { label: data.database.name, to: databasePath(data.database.id) },
          { label: `${data.schema}.${data.name}`, to: structure },
          { label: 'Data' },
        ]}
      />
      <PageHeader
        title={
          <>
            Data <strong className="font-bold">{data.name}</strong>
          </>
        }
        action={
          <Link to={structure} className="btn-secondary">
            <Icon name="columns" />
            Lihat struktur
          </Link>
        }
      />

      {/* Error saat pindah halaman ditampilkan di atas data lama, bukan menggantikannya. */}
      {error && (
        <div className="mb-4">
          <ErrorMessage error={error} />
        </div>
      )}

      <div className="mb-3.5 flex flex-wrap items-center gap-2">
        <span className="chip">
          <Icon name="hash" className="h-[1.15em] w-[1.15em] text-green" />
          <strong className="text-ink">{data.rows.length}</strong> baris di halaman ini
        </span>
        <span className="chip">
          <Icon name="columns" className="h-[1.15em] w-[1.15em] text-green" />
          <strong className="text-ink">{data.columns.length}</strong> kolom
        </span>
        <span className="chip">
          ukuran halaman <strong className="text-ink">{data.page_size}</strong>
        </span>
      </div>

      {data.rows.length === 0 ? (
        <EmptyState
          icon="table"
          title={page > 1 ? 'Halaman ini kosong' : 'Tabel ini kosong'}
          message={page > 1 ? 'Tidak ada baris di halaman ini.' : 'Belum ada baris di tabel ini.'}
        />
      ) : (
        <div
          ref={tableRef}
          className={'table-wrap max-h-[68vh] overflow-auto transition-opacity ' + (isPlaceholderData ? 'opacity-60' : '')}
          aria-busy={isFetching}
        >
          <table className="data-table">
            <thead>
              <tr>
                {data.columns.map((col, i) => (
                  <th key={i}>{col}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {data.rows.map((row, r) => (
                <tr key={r}>
                  {row.map((cell, c) => (
                    <td key={c} title={cell ?? 'NULL'}>
                      {cell === null ? <span className="null-pill">NULL</span> : cell}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <Pager page={data.page} hasNext={data.has_next} onChange={goTo} disabled={isFetching} />
    </>
  )
}
