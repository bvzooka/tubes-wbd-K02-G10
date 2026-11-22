import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { useSession } from '../hooks/useSession'
import ErrorMessage from './ErrorMessage'
import Loading from './Loading'

interface RequireAuthProps {
  admin?: boolean
}

export default function RequireAuth({ admin = false }: RequireAuthProps) {
  const location = useLocation()
  const { data: session, isLoading, error } = useSession()

  if (isLoading) return <Loading />
  if (error) return <ErrorMessage error={error} />
  if (!session) {
    return <Navigate to={'/login?next=' + encodeURIComponent(location.pathname + location.search)} replace />
  }
  if (admin && !session.user.is_platform_admin) {
    return <ErrorMessage error={new Error('Halaman ini hanya untuk Platform Admin.')} />
  }
  return <Outlet />
}
