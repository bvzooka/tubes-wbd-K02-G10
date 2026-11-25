import type { AccessLevel, DBMS } from '../../api/types'
const seg = encodeURIComponent

export function databasePath(dbID: number | string) {
  return `/databases/${dbID}`
}

export function tablePath(dbID: number | string, schema: string, table: string) {
  return `/databases/${dbID}/tables/${seg(schema)}/${seg(table)}`
}

export function tableDataPath(dbID: number | string, schema: string, table: string) {
  return tablePath(dbID, schema, table) + '/data'
}

// formatRows menulis perkiraan jumlah baris dengan pemisah ribuan Indonesia.
// null berarti database tidak tahu (misalnya view), ditulis sebagai tanda pisah.
export function formatRows(n: number | null): string {
  return n === null ? '–' : n.toLocaleString('id-ID')
}

export const accessLabel: Record<AccessLevel, string> = {
  view_only: 'View only',
  view_edit: 'View + Edit',
}

export const dbmsLabel: Record<DBMS, string> = {
  postgres: 'PostgreSQL',
  mysql: 'MySQL',
  mariadb: 'MariaDB',
}

export function explainPath(dbID: number | string) {
  return `/databases/${dbID}/explain`
}
