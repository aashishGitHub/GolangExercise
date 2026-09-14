import { useCallback, useState } from 'react'
import { confirmSignUp, fetchAuthSession, getCurrentUser, signIn, signOut, signUp } from 'aws-amplify/auth'

// cognito-local doesn't support SRP (mirrors offline-sync-app's finding) —
// sign-in must explicitly request USER_PASSWORD_AUTH.
export function useAuth() {
  const [email, setEmail] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const register = useCallback(async (username: string, password: string) => {
    setError(null)
    try {
      await signUp({ username, password, options: { userAttributes: { email: username } } })
    } catch (e) {
      setError(String(e))
      throw e
    }
  }, [])

  const confirm = useCallback(async (username: string, code: string) => {
    setError(null)
    try {
      await confirmSignUp({ username, confirmationCode: code })
    } catch (e) {
      setError(String(e))
      throw e
    }
  }, [])

  const login = useCallback(async (username: string, password: string) => {
    setError(null)
    try {
      await signIn({ username, password, options: { authFlowType: 'USER_PASSWORD_AUTH' } })
      const user = await getCurrentUser()
      setEmail(user.username)
    } catch (e) {
      setError(String(e))
      throw e
    }
  }, [])

  const logout = useCallback(async () => {
    await signOut()
    setEmail(null)
  }, [])

  /** The bearer token every API call needs — server-authoritative session,
   * never a client-side claim about who's logged in. */
  const idToken = useCallback(async (): Promise<string> => {
    const session = await fetchAuthSession()
    const token = session.tokens?.idToken?.toString()
    if (!token) throw new Error('not signed in')
    return token
  }, [])

  return { email, error, register, confirm, login, logout, idToken }
}
