import { useCallback, useRef, useState } from 'react'
import './amplifyConfig'
import { AuthPanel } from './auth/AuthPanel'
import { useAuth } from './auth/useAuth'
import { EventPage } from './EventPage'

export function App() {
  const auth = useAuth()
  const [authPanelOpen, setAuthPanelOpen] = useState(false)
  const [authPanelMode, setAuthPanelMode] = useState<'signIn' | 'signUp'>('signIn')
  // Set by EventPage via onAuthRequired when a signed-out visitor tries to
  // reserve seats: the seat selection lives in EventPage's own state and is
  // untouched by the sign-in detour, so re-invoking the same handler here
  // resumes exactly where the visitor left off.
  const pendingRetryRef = useRef<(() => void) | null>(null)

  const openAuthPanel = useCallback(
    (mode: 'signIn' | 'signUp') => {
      auth.clearError()
      setAuthPanelMode(mode)
      setAuthPanelOpen(true)
    },
    [auth],
  )

  const closeAuthPanel = useCallback(() => {
    setAuthPanelOpen(false)
    pendingRetryRef.current = null
    auth.clearError()
  }, [auth])

  const requireAuth = useCallback(
    (retry: () => void) => {
      pendingRetryRef.current = retry
      auth.clearError()
      setAuthPanelMode('signIn')
      setAuthPanelOpen(true)
    },
    [auth],
  )

  const handleAuthenticated = useCallback(() => {
    setAuthPanelOpen(false)
    const retry = pendingRetryRef.current
    pendingRetryRef.current = null
    retry?.()
  }, [])

  if (auth.status === 'loading') {
    return <p>Loading…</p>
  }

  return (
    <div>
      <header>
        <h1>Ticketing</h1>
        {auth.status === 'authed' ? (
          <p>
            Signed in as {auth.email}{' '}
            <button type="button" onClick={() => auth.logout()}>
              Sign out
            </button>
          </p>
        ) : (
          !authPanelOpen && (
            <p>
              <button type="button" onClick={() => openAuthPanel('signIn')}>
                Sign in
              </button>{' '}
              <button type="button" onClick={() => openAuthPanel('signUp')}>
                Create account
              </button>
            </p>
          )
        )}
      </header>

      {authPanelOpen && auth.status !== 'authed' && (
        <AuthPanel auth={auth} initialMode={authPanelMode} onAuthenticated={handleAuthenticated} onCancel={closeAuthPanel} />
      )}

      {/* Browsing and the seat map never require an account — only the API's
       * hold/order routes do (internal/httpapi/router.go). EventPage gates
       * just the reserve actions via onAuthRequired. */}
      <EventPage eventId={1} idToken={auth.idToken} isAuthed={auth.status === 'authed'} onAuthRequired={requireAuth} />
    </div>
  )
}
