import { Link } from 'react-router-dom'
import PageHeader from '../components/PageHeader'

export default function NotFoundPage() {
  return (
    <PageHeader
      title="Halaman tidak ditemukan"
      subtitle="Alamat ini tidak ada atau belum tersedia."
      action={
        <Link to="/databases" className="btn-secondary">
          Ke daftar database
        </Link>
      }
    />
  )
}
