import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { DatabaseItem } from '../../api/types'
import Badge from '../../components/Badge'
import EmptyState from '../../components/EmptyState'
import ErrorMessage from '../../components/ErrorMessage'
import Icon from '../../components/Icon'
import Loading from '../../components/Loading'
import PageHeader from '../../components/PageHeader'
import StatCard from '../../components/StatCard'
import { useSession } from '../../hooks/useSession'
import { accessLabel, databasePath, dbmsLabel } from './paths'

export default function DatabasesPage() {
  const { data: session } = useSession()
  const { data: databases, isLoading, error } = useQuery({
    queryKey: ['databases'],
    queryFn: () => api<DatabaseItem[]>('GET', '/databases'),
  })

  return (
    <>
      <PageHeader
        eyebrow={{ icon: 'bolt', label: 'Workspace' }}
        title={
          <>
            Halo, <strong className="font-bold">{session?.user.username}</strong>
          </>
        }
        subtitle="Pilih database untuk melihat struktur dan datanya. Kartu gelap berarti kamu punya hak View + Edit."
        aside={databases && <StatCard icon="db" label="Database tersedia" value={databases.length} />}
      />

      {isLoading && <Loading />}
      <ErrorMessage error={error} />

      {databases?.length === 0 && (
        <EmptyState
          icon="lock"
          title="Belum ada database"
          message="Kamu belum punya akses ke database mana pun. Minta Platform Admin menambahkan group kamu ke sebuah database."
        />
      )}

      {databases && databases.length > 0 && (
        <ul className="m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(270px,1fr))] gap-4 p-0">
          {databases.map((db) => (
            <li key={db.id} className={'tile' + (db.can_edit ? ' is-dark' : '')}>
              <div className="flex items-center justify-between gap-3">
                <span className="tile-icon">
                  <Icon name="db" className="h-[22px] w-[22px]" />
                </span>
                <Badge tone="outline">{dbmsLabel[db.dbms] ?? db.dbms}</Badge>
              </div>
              <h2 className="m-0 break-words text-[1.2rem] font-bold tracking-[-0.02em] text-inherit">
                <Link to={databasePath(db.id)} className="tile-link">
                  {db.name}
                </Link>
              </h2>
              <div className="flex gap-1.5" title={accessLabel[db.access_level]}>
                <span className="access-seg on">
                  <Icon name="eye" />
                  Lihat
                </span>
                <span className={'access-seg' + (db.can_edit ? ' on' : '')}>
                  <Icon name="edit" />
                  Edit
                </span>
                <span className="sr-only">Akses: {accessLabel[db.access_level]}</span>
              </div>
              <div className="mt-auto flex items-center justify-end">
                <span className="tile-go" aria-hidden="true">
                  <Icon name="arrow" className="h-5 w-5" />
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </>
  )
}
