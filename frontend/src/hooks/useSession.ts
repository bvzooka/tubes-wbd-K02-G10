import { useQuery } from '@tanstack/react-query'
import { api, ApiError, setCsrfToken } from '../api/client'
import type { Session } from '../api/types'

export const sessionKey = ['session']

// useSession mengembalikan null bila belum login (401), bukan error.
export function useSession() {
  return useQuery({
    queryKey: sessionKey,
    queryFn: async () => {
      try {
        const session = await api<Session>('GET', '/auth/me')
        setCsrfToken(session.csrf_token)
        return session
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) return null
        throw e
      }
    },
    staleTime: 60_000,
  })
}
