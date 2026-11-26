import { Navigate, Route, Routes } from 'react-router-dom'
import Layout from './components/Layout'
import RequireAuth from './components/RequireAuth'
import NotFoundPage from './pages/NotFoundPage'
import LoginPage from './pages/auth/LoginPage'
import GroupDetailPage from './pages/admin/GroupDetailPage'
import GroupFormPage from './pages/admin/GroupFormPage'
import GroupsPage from './pages/admin/GroupsPage'
import UserFormPage from './pages/admin/UserFormPage'
import UsersPage from './pages/admin/UsersPage'
import ApprovalsPage from './pages/approvals/ApprovalsPage'
import ConnectionFormPage from './pages/connections/ConnectionFormPage'
import ConnectionsPage from './pages/connections/ConnectionsPage'
import DatabasesPage from './pages/databases/DatabasesPage'
import ExplainPage from './pages/databases/ExplainPage'
import SchemaPage from './pages/databases/SchemaPage'
import TableDataPage from './pages/databases/TableDataPage'
import TablePage from './pages/databases/TablePage'
import ExecutionPage from './pages/migrations/ExecutionPage'
import HistoryPage from './pages/migrations/HistoryPage'
import MigrationDetailPage from './pages/migrations/MigrationDetailPage'
import MigrationFormPage from './pages/migrations/MigrationFormPage'
import MigrationsPage from './pages/migrations/MigrationsPage'
import SchemaDiffPage from './pages/schemadiff/SchemaDiffPage'

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />

      <Route element={<RequireAuth />}>
        <Route element={<Layout />}>
          {/* Sama seperti M1: halaman awal setelah login adalah daftar database. */}
          <Route index element={<Navigate to="/databases" replace />} />
          <Route path="databases" element={<DatabasesPage />} />
          <Route path="databases/:id" element={<SchemaPage />} />
          <Route path="databases/:id/explain" element={<ExplainPage />} />
          <Route path="databases/:id/tables/:schema/:table" element={<TablePage />} />
          <Route path="databases/:id/tables/:schema/:table/data" element={<TableDataPage />} />
          <Route path="databases/:id/migrations" element={<MigrationsPage />} />
          <Route path="databases/:id/migrations/new" element={<MigrationFormPage />} />
          <Route path="databases/:id/migrations/:version/edit" element={<MigrationFormPage />} />
          <Route path="databases/:id/migrations/:version" element={<MigrationDetailPage />} />
          <Route path="databases/:id/history" element={<HistoryPage />} />
          <Route path="databases/:id/schema-diff" element={<SchemaDiffPage />} />
          <Route path="executions/:executionID" element={<ExecutionPage />} />
          <Route path="approvals" element={<ApprovalsPage />} />

          <Route path="admin" element={<RequireAuth admin />}>
            <Route path="users" element={<UsersPage />} />
            <Route path="users/new" element={<UserFormPage />} />
            <Route path="users/:id" element={<UserFormPage />} />
            <Route path="groups" element={<GroupsPage />} />
            <Route path="groups/new" element={<GroupFormPage />} />
            <Route path="groups/:id" element={<GroupDetailPage />} />
            <Route path="connections" element={<ConnectionsPage />} />
            <Route path="connections/new" element={<ConnectionFormPage />} />
            <Route path="connections/:id" element={<ConnectionFormPage />} />
          </Route>

          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Route>
    </Routes>
  )
}
