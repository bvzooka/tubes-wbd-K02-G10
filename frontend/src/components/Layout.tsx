import { useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import { useSession } from '../hooks/useSession'

interface NavItem {
  to: string
  label: string
  adminOnly?: boolean
}

const navItems: NavItem[] = [
  { to: '/databases', label: 'Database' },
  { to: '/approvals', label: 'Persetujuan' },
  { to: '/admin/users', label: 'User', adminOnly: true },
  { to: '/admin/groups', label: 'Group', adminOnly: true },
  { to: '/admin/connections', label: 'Koneksi', adminOnly: true },
]

export default function Layout() {
  const [open, setOpen] = useState(false)
  const { data: session } = useSession()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const isAdmin = session?.user.is_platform_admin ?? false

  async function logout() {
    await api('POST', '/auth/logout')
    queryClient.clear()
    navigate('/login')
  }

  const links = navItems
    .filter((item) => !item.adminOnly || isAdmin)
    .map((item) => (
      <NavLink
        key={item.to}
        to={item.to}
        onClick={() => setOpen(false)}
        className={({ isActive }) =>
          'flex min-h-[44px] items-center rounded-md px-3 text-sm ' +
          (isActive ? 'bg-indigo-50 font-medium text-indigo-700' : 'text-slate-700 hover:bg-slate-100')
        }
      >
        {item.label}
      </NavLink>
    ))

  return (
    <div className="min-h-screen">
      <header className="flex h-14 items-center justify-between border-b border-slate-200 bg-white px-4">
        <button className="btn-secondary md:hidden" onClick={() => setOpen(!open)} aria-label="Menu">
          ☰
        </button>
        <span className="font-semibold text-slate-900">DB Platform</span>
        <div className="flex items-center gap-3 text-sm">
          <span className="hidden text-slate-600 sm:inline">{session?.user.username}</span>
          <button className="btn-secondary" onClick={logout}>
            Keluar
          </button>
        </div>
      </header>
      <div className="flex">
        <nav
          className={
            'fixed inset-y-14 left-0 z-10 w-56 space-y-1 border-r border-slate-200 bg-white p-3 md:static md:block ' +
            (open ? 'block' : 'hidden')
          }
        >
          {links}
        </nav>
        <main className="min-w-0 flex-1 p-4 md:p-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
