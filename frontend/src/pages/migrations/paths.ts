export function migrationsPath(dbID: number | string) {
  return `/databases/${dbID}/migrations`
}

export function newMigrationPath(dbID: number | string) {
  return migrationsPath(dbID) + '/new'
}

// form draft baru yang isinya diawali dari versi lain
export function copyMigrationPath(dbID: number | string, version: number | string) {
  return `${newMigrationPath(dbID)}?from=${version}`
}

export function schemaDiffPath(dbID: number | string) {
  return `/databases/${dbID}/schema-diff`
}

export function migrationPath(dbID: number | string, version: number | string) {
  return `${migrationsPath(dbID)}/${version}`
}

export function historyPath(dbID: number | string) {
  return `/databases/${dbID}/history`
}

export function executionPath(executionID: number | string) {
  return `/executions/${executionID}`
}

export function editMigrationPath(dbID: number | string, version: number | string) {
  return `${migrationsPath(dbID)}/${version}/edit`
}

// dipakai sebagai kunci cache TanStack Query, awalan yang sama membuat semuanya bisa disegarkan sekaligus
export const migrationsKey = (dbID: string) => ['migrations', dbID]
