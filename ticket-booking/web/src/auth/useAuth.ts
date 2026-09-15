import { useCallback, useEffect, useState } from 'react'
import { confirmSignUp, fetchAuthSession, getCurrentUser, signIn, signOut, signUp } from 'aws-amplify/auth'
import { describeAuthError, type AuthErrorInfo } from './authErrorCopy'

export type AuthStatus = 'loading' | 'authed' | 'anon'

// cognito-local doesn't support SRP (mirrors offline-sync-app's finding) —
// sign-in must explicitly request USER_PASSWORD_AUTH.
export function useAuth() {
  // 'loading' until the mount-time session check resolves, so the UI never
  // flashes a sign-in form for a user who is already authenticated — and so
  // a page reload no longer silently drops a valid session (the bug this
  // was built to fix: `signedIn` used to be plain useState(false)).
  const [status, setStatus] = useState<AuthStatus>('loading')
  const [email, setEmail] = useState<string | null>(null)
  const [error, setError] = useState<AuthErrorInfo | null>(null)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const session = await fetchAuthSession()
        if (cancelled) return
        if (session.tokens?.idToken) {
          const user = await getCurrentUser()
          if (cancelled) return
          setEmail(user.username)
          setStatus('authed')
        } else {
          setStatus('anon')
        }
      } catch {
        if (!cancelled) setStatus('anon')
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  const register = useCallback(async (username: string, password: string) => {
    setError(null)
    try {
      await signUp({ username, password, options: { userAttributes: { email: username } } })
    } catch (e) {
      setError(describeAuthError(e))
      throw e
    }
  }, [])

  const confirm = useCallback(async (username: string, code: string) => {
    setError(null)
    try {
      await confirmSignUp({ username, confirmationCode: code })
    } catch (e) {
      setError(describeAuthError(e))
      throw e
    }
  }, [])

  const login = useCallback(async (username: string, password: string) => {
    setError(null)
    try {
      await signIn({ username, password, options: { authFlowType: 'USER_PASSWORD_AUTH' } })
      const user = await getCurrentUser()
      setEmail(user.username)
      setStatus('authed')
    } catch (e) {
      setError(describeAuthError(e))
      throw e
    }
  }, [])

  const logout = useCallback(async () => {
    await signOut()
    setEmail(null)
    setStatus('anon')
  }, [])

  const clearError = useCallback(() => setError(null), [])

  /** The bearer token every API call needs — server-authoritative session,
   * never a client-side claim about who's logged in. */
  const idToken = useCallback(async (): Promise<string> => {
    const session = await fetchAuthSession()
    const token = session.tokens?.idToken?.toString()
    if (!token) throw new Error('not signed in')
    return token
  }, [])

  return { status, email, error, register, confirm, login, logout, idToken, clearError }
}

export type UseAuthReturn = ReturnType<typeof useAuth>
