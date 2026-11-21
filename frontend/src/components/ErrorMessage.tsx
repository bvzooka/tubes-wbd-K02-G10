import { ApiError } from '../api/client'

interface ErrorMessageProps {
  error: unknown
}

export default function ErrorMessage({ error }: ErrorMessageProps) {
  if (!error) return null
  const message = error instanceof Error ? error.message : 'Terjadi kesalahan.'
  const requestId = error instanceof ApiError ? error.requestId : ''
  return (
    <div role="alert" className="rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700">
      {message}
      {requestId && <span className="mt-1 block text-xs text-red-500">ID request: {requestId}</span>}
    </div>
  )
}
