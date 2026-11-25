export type AccessLevel = 'view_only' | 'view_edit'

export interface SessionUser {
  id: number
  username: string
  is_platform_admin: boolean
}

export interface Session {
  user: SessionUser
  csrf_token: string
}

export interface User {
  id: number
  username: string
  is_platform_admin: boolean
  is_active: boolean
  created_at: string
}

export interface Group {
  id: number
  name: string
  description: string
  leader_id: number | null
  leader_name?: string
  member_count: number
  access_count: number
  created_at: string
}

export interface GroupMember {
  user_id: number
  username: string
  is_active: boolean
  is_leader: boolean
}

export interface GroupAccess {
  connection_id: number
  connection_name: string
  access_level: AccessLevel
}

export interface Option {
  id: number
  name: string
}

export interface GroupDetail extends Group {
  members: GroupMember[]
  accesses: GroupAccess[]
  available_users: Option[]
  available_connections: Option[]
  can_delete: boolean
}

export interface InboxItem {
  execution_id: number
  connection_id: number
  database: string
  version: number
  kind: 'apply' | 'rollback' | 'forward'
  mode: 'normal' | 'batch'
  sql: string
  requested_by: string
  requested_at: string
  group_id: number
  group_name: string
  status: string
}

// Koneksi dan viewer. Bentuknya mengikuti struct JSON di
// internal/connmgr/api_handler.go dan internal/viewer/api_handler.go.

export type DBMS = 'postgres' | 'mysql' | 'mariadb'

export type SSLMode = 'disable' | 'allow' | 'prefer' | 'require' | 'verify-ca' | 'verify-full'

// Connection tidak pernah membawa password, bahkan yang terenkripsi.
export interface Connection {
  id: number
  name: string
  dbms: DBMS
  host: string
  port: number
  database_name: string
  db_username: string
  ssl_mode: SSLMode
  is_active: boolean
  created_at: string
  updated_at: string
}

// ConnectionInput adalah isian form. Password kosong saat update berarti tidak diubah.
export interface ConnectionInput {
  name: string
  dbms: DBMS
  host: string
  port: number
  database_name: string
  db_username: string
  password: string
  ssl_mode: SSLMode
}

// ConnectionTestInput menambah id saat mengedit, supaya backend memakai password tersimpan.
export interface ConnectionTestInput extends ConnectionInput {
  id?: number
}

// TestResult: gagal terhubung dikembalikan 200 dengan ok=false, bukan error HTTP.
export interface TestResult {
  ok: boolean
  message: string
}

// DatabaseItem: can_edit hanya untuk UI, pengamanan tetap di backend.
export interface DatabaseItem {
  id: number
  name: string
  dbms: DBMS
  access_level: AccessLevel
  can_edit: boolean
}

export type TableKind = 'table' | 'view'

export interface TableItem {
  schema: string
  name: string
  kind: TableKind
  // null bila database tidak tahu perkiraan jumlah baris.
  estimated_rows: number | null
}

export interface SchemaGroup {
  name: string
  tables: TableItem[]
}

export interface SchemaSummary {
  database: DatabaseItem
  schemas: SchemaGroup[]
}

export interface Column {
  name: string
  position: number
  data_type: string
  nullable: boolean
  // null berarti kolom tanpa default, berbeda dengan default string kosong.
  default: string | null
}

export type ConstraintType = 'primary_key' | 'foreign_key' | 'unique' | 'check'

// Field ref_* dan on_* hanya ada untuk foreign key, check_expr hanya untuk check.
export interface Constraint {
  name: string
  type: ConstraintType
  columns: string[]
  ref_schema?: string
  ref_table?: string
  ref_columns?: string[]
  on_update?: string
  on_delete?: string
  check_expr?: string
}

export interface Index {
  name: string
  method: string
  columns: string[]
  unique: boolean
  primary: boolean
  definition: string
}

export interface TableDetail {
  database: DatabaseItem
  schema: string
  name: string
  kind: TableKind
  estimated_rows: number | null
  columns: Column[]
  constraints: Constraint[]
  indexes: Index[]
}

// TableData: sel NULL dikirim sebagai null, bukan string kosong.
export interface TableData {
  database: DatabaseItem
  schema: string
  name: string
  columns: string[]
  rows: (string | null)[][]
  page: number
  page_size: number
  has_next: boolean
}

export interface PlanNode {
  depth: number
  node_type: string
  relationship?: string
  relation?: string
  alias?: string
  index_name?: string
  join_type?: string
  condition?: string
  startup_cost: number
  total_cost: number
  plan_rows: number
  plan_width: number
  analyzed: boolean
  actual_startup_ms: number
  actual_total_ms: number
  actual_rows: number
  actual_loops: number
  shared_hit: number
  shared_read: number
}

export interface ExplainResult {
  sql: string
  analyze: boolean
  dialect: DBMS
  plan: {
    total_cost: number
    planning_time_ms: number | null
    execution_time_ms: number | null
    nodes: PlanNode[]
    json: string
  } | null
  raw_text: string
}
