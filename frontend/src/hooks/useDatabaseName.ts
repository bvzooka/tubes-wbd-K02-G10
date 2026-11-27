import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import type { DatabaseItem } from '../api/types'

// nama diambil dari daftar database, cache-nya sama dengan halaman Database
export function useDatabaseName(id: number | string | undefined) {
  const databases = useQuery({ queryKey: ['databases'], queryFn: () => api<DatabaseItem[]>('GET', '/databases') })
  return databases.data?.find((d) => String(d.id) === String(id))?.name ?? `Database ${id}`
}
