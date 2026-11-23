import { ApiError } from '../api/client'

interface ErrorMessageProps {
  error: unknown
}

// ErrorMessage memakai gaya alert-error M1 (bulatan "!" merah) dan menampilkan
// request_id supaya error bisa dicari di log server.
export default function ErrorMessage({ error }: ErrorMessageProps) {
  if (!error) return null
  const message = error instanceof Error ? error.message : 'Terjadi kesalahan.'
  const requestId = error instanceof ApiError ? error.requestId : ''
  return (
    <div role="alert" className="alert alert-error">
      <span className="alert-mark" aria-hidden="true">
        !
      </span>
      <span>
        {message}
        {requestId && <span className="mt-0.5 block text-xs opacity-75">ID request: {requestId}</span>}
      </span>
    </div>
  )
}
