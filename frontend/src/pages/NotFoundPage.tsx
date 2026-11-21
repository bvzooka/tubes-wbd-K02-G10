import { Link } from 'react-router-dom'
import EmptyState from '../components/EmptyState'

export default function NotFoundPage() {
  return (
    <div className="text-center">
      <EmptyState message="Halaman tidak ditemukan atau belum tersedia." />
      <Link to="/" className="btn-secondary">
        Kembali
      </Link>
    </div>
  )
}
