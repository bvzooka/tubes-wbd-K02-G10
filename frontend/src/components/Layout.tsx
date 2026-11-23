import { useEffect, useState } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import { useSession } from '../hooks/useSession'
import Icon, { type IconName } from './Icon'

interface NavItem {
  to: string
  label: string
  icon: IconName
}

const mainNav: NavItem[] = [
  { to: '/databases', label: 'Database', icon: 'db' },
  { to: '/approvals', label: 'Persetujuan', icon: 'inbox' },
]

// Menu admin hanya kosmetik; backend tetap menolak non-admin (403).
const adminNav: NavItem[] = [
  { to: '/admin/users', label: 'Pengguna', icon: 'users' },
  { to: '/admin/groups', label: 'Group', icon: 'grid' },
  { to: '/admin/connections', label: 'Koneksi', icon: 'plug' },
]

function NavItemLink({ item }: { item: NavItem }) {
  return (
    <NavLink
      to={item.to}
      className={({ isActive }) =>
        'group flex min-h-[44px] items-center gap-3 rounded-full px-4 font-semibold no-underline transition-colors hover:no-underline ' +
        (isActive ? 'bg-ink text-white' : 'text-ink-2 hover:bg-green-soft')
      }
    >
      {({ isActive }) => (
        <>
          <Icon name={item.icon} className={'h-5 w-5 ' + (isActive ? 'text-lime' : 'text-green')} />
          {item.label}
        </>
      )}
    </NavLink>
  )
}

export default function Layout() {
  const [open, setOpen] = useState(false)
  const { data: session } = useSession()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const location = useLocation()
  const isAdmin = session?.user.is_platform_admin ?? false

  // Drawer mobile tertutup setiap kali halaman berganti.
  useEffect(() => setOpen(false), [location.pathname])

  // Esc menutup drawer.
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])

  async function logout() {
    await api('POST', '/auth/logout')
    queryClient.clear()
    navigate('/login')
  }

  return (
    <div className="min-h-screen pt-3 max-md:pt-2">
      {/* Header kaca menempel di atas. z-30: di atas sidebar desktop (z-20), di bawah drawer mobile (z-50). */}
      <header className="sticky top-3 z-30 mx-3 max-md:top-2 max-md:mx-2 md:mx-auto md:max-w-content md:px-6">
        <div className="glass flex items-center justify-between gap-3 rounded-full py-2 pl-3 pr-2.5">
          <div className="flex min-w-0 items-center gap-2">
            <button
              type="button"
              className="icon-btn md:hidden"
              onClick={() => setOpen(true)}
              aria-label="Buka menu"
              aria-expanded={open}
              aria-controls="app-nav"
            >
              <Icon name="menu" className="h-5 w-5" />
            </button>
            <Link
              to="/databases"
              aria-label="Database Management Platform, ke daftar database"
              className="flex min-h-[44px] min-w-0 items-center gap-2.5 rounded-full pr-2 font-extrabold tracking-[-0.02em] text-ink no-underline hover:no-underline"
            >
              <span className="grid h-11 w-11 flex-none place-items-center overflow-hidden rounded-md bg-[#f4fdf1]">
                <img src="/logo.png" alt="" width={44} height={44} className="h-[108%] w-[108%] max-w-none" />
              </span>
              <span className="truncate max-sm:sr-only" aria-hidden="true">
                Database Management Platform
              </span>
            </Link>
          </div>

          <div className="flex flex-none items-center gap-2.5">
            <span className="avatar" aria-hidden="true" />
            <span className="grid leading-tight text-ink max-md:hidden">
              <strong className="text-[0.92rem] font-bold">{session?.user.username}</strong>
              <small className="text-[0.76rem] text-muted">
                {isAdmin ? 'Platform Admin' : 'Pengguna'}
              </small>
            </span>
            <button
              type="button"
              className="icon-btn ml-1 hover:border-danger hover:bg-danger-soft hover:text-danger"
              onClick={logout}
              aria-label="Keluar"
              title="Keluar"
            >
              <Icon name="logout" className="h-5 w-5" />
            </button>
          </div>
        </div>
      </header>

      {/*
        Baris konten minimal setinggi layar dikurangi header (74px: padding atas 12 + pil 62; padding, bukan margin, supaya tidak "bocor" ke body) dan tanpa padding bawah di
        luarnya. Sidebar sticky tidak boleh melewati batas bawah baris ini; kalau baris lebih
        pendek dari layar, sidebar terdorong ke atas dan menimpa header.
      */}
      <div className="mx-auto flex max-w-content gap-6 px-6 pt-7 max-md:px-4 max-md:pt-5 md:min-h-[calc(100vh-74px)]">
        {/* Lapisan gelap di belakang drawer mobile; ketuk untuk menutup. */}
        <div
          className={
            'fixed inset-0 z-40 bg-ink/30 transition-opacity duration-200 md:hidden ' +
            (open ? 'opacity-100' : 'pointer-events-none opacity-0')
          }
          onClick={() => setOpen(false)}
          aria-hidden="true"
        />
        <aside
          id="app-nav"
          className={
            // Desktop: kartu kaca yang diam di bawah header dan punya scroll sendiri bila menunya
            // lebih tinggi dari layar; konten di kanan scroll mengikuti halaman.
            // Mobile: drawer dari kiri; invisible saat tertutup supaya tidak bisa di-Tab.
            'glass scroll-thin w-60 flex-none self-start overflow-y-auto overscroll-contain rounded-xl p-2.5 ' +
            'md:sticky md:top-[92px] md:z-20 md:max-h-[calc(100vh-108px)] ' +
            'max-md:fixed max-md:inset-y-2 max-md:left-2 max-md:z-50 max-md:w-[min(18rem,calc(100vw-4rem))] max-md:bg-white/90 max-md:transition-[transform,visibility] max-md:duration-300 max-md:ease-[cubic-bezier(0.32,0.72,0,1)] ' +
            (open ? 'max-md:visible max-md:translate-x-0' : 'max-md:invisible max-md:-translate-x-[110%]')
          }
        >
          <div className="mb-2 flex items-center justify-between pl-3 md:hidden">
            <span className="font-bold text-ink">Menu</span>
            <button type="button" className="icon-btn" onClick={() => setOpen(false)} aria-label="Tutup menu">
              <Icon name="close" className="h-5 w-5" />
            </button>
          </div>
          <nav aria-label="Navigasi utama" className="space-y-1">
            {mainNav.map((item) => (
              <NavItemLink key={item.to} item={item} />
            ))}
            {isAdmin && (
              <>
                <p className="px-4 pb-1 pt-4 text-[0.8rem] font-semibold text-muted">Administrasi</p>
                {adminNav.map((item) => (
                  <NavItemLink key={item.to} item={item} />
                ))}
              </>
            )}
          </nav>
        </aside>

        <main className="min-w-0 flex-1 pb-12">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
