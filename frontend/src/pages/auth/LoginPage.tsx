import { useState, type FormEvent } from 'react'
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { api, setCsrfToken } from '../../api/client'
import type { Session } from '../../api/types'
import ErrorMessage from '../../components/ErrorMessage'
import { sessionKey, useSession } from '../../hooks/useSession'

// safeNext hanya menerima path lokal agar tidak bisa dipakai untuk open redirect.
function safeNext(next: string | null) {
  return next && next.startsWith('/') && !next.startsWith('//') ? next : '/'
}

export default function LoginPage() {
  const [params] = useSearchParams()
  const next = safeNext(params.get('next'))
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { data: session } = useSession()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<unknown>(null)
  const [submitting, setSubmitting] = useState(false)

  if (session) return <Navigate to={next} replace />

  async function submit(e: FormEvent) {
    e.preventDefault()
    setSubmitting(true)
    setError(null)
    try {
      const { csrf_token } = await api<{ csrf_token: string }>('GET', '/auth/csrf')
      setCsrfToken(csrf_token)
      const result = await api<Session>('POST', '/auth/login', { username, password })
      setCsrfToken(result.csrf_token)
      queryClient.setQueryData(sessionKey, result)
      navigate(next, { replace: true })
    } catch (err) {
      setError(err)
      setPassword('')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center p-4">
      <form onSubmit={submit} className="card w-full max-w-sm space-y-4">
        <h1 className="text-xl font-semibold text-slate-900">Masuk</h1>
        <ErrorMessage error={error} />
        <label className="block text-sm">
          Username
          <input
            className="input mt-1"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            required
          />
        </label>
        <label className="block text-sm">
          Password
          <input
            className="input mt-1"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </label>
        <button className="btn-primary w-full" disabled={submitting}>
          {submitting ? 'Memproses…' : 'Masuk'}
        </button>
      </form>
    </div>
  )
}
