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
