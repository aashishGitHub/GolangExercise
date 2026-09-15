import { useState, type FormEvent } from 'react'
import type { UseAuthReturn } from './useAuth'

// cognito-local always accepts this fixed confirmation code (see
// docker-compose.yml's `CODE: "123456"`) — prefilled so local dev never
// has to go looking for it, with the real-world step named explicitly.
const LOCAL_DEV_CODE = '123456'

type Mode = 'signUp' | 'confirm' | 'signIn'

interface Props {
  auth: UseAuthReturn
  initialMode: 'signIn' | 'signUp'
  onAuthenticated: () => void
  onCancel: () => void
}

/** The account panel: create account -> confirm email -> sign in, plus
 * direct sign-in for a returning user. One component so the typed email
 * carries across mode switches (e.g. "Sign in instead" after a duplicate
 * signup) without re-plumbing state through the parent. */
export function AuthPanel({ auth, initialMode, onAuthenticated, onCancel }: Props) {
  const [mode, setMode] = useState<Mode>(initialMode)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState(LOCAL_DEV_CODE)
  const [busy, setBusy] = useState(false)

  function switchMode(next: Mode) {
    auth.clearError()
    setMode(next)
  }

  async function handleSignUp(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await auth.register(username, password)
      setMode('confirm')
    } catch {
      // auth.error is already set with copy for this failure; on a
      // duplicate account it offers its own "Sign in instead" action below.
    } finally {
      setBusy(false)
    }
  }

  async function handleConfirm(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await auth.confirm(username, code)
      setMode('signIn')
    } catch {
      // stays on this screen; auth.error shown below
    } finally {
      setBusy(false)
    }
  }

  async function handleSignIn(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await auth.login(username, password)
      onAuthenticated()
    } catch {
      // stays on this screen; auth.error shown below
    } finally {
      setBusy(false)
    }
  }

  return (
    <section aria-label={mode === 'signUp' ? 'Create account' : mode === 'confirm' ? 'Confirm email' : 'Sign in'}>
      {mode === 'signUp' && (
        <form onSubmit={handleSignUp}>
          <h2>Create your account</h2>
          <div>
            <label htmlFor="auth-username">Email address</label>
            <br />
            <input
              id="auth-username"
              type="email"
              autoComplete="email"
              required
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </div>
          <div>
            <label htmlFor="auth-password">Password</label>
            <br />
            <input
              id="auth-password"
              type="password"
              autoComplete="new-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>
          <button type="submit" disabled={busy}>
            {busy ? 'Creating account…' : 'Create account'}
          </button>
          <p>
            Already have an account?{' '}
            <button type="button" onClick={() => switchMode('signIn')}>
              Sign in instead
            </button>
          </p>
        </form>
      )}

      {mode === 'confirm' && (
        <form onSubmit={handleConfirm}>
          <h2>Confirm your email</h2>
          <p>
            We sent a confirmation code to {username || 'your email'}. (Local dev: cognito-local always
            issues {LOCAL_DEV_CODE}, already filled in below.)
          </p>
          <div>
            <label htmlFor="auth-code">Confirmation code</label>
            <br />
            <input
              id="auth-code"
              type="text"
              inputMode="numeric"
              autoComplete="one-time-code"
              required
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
          </div>
          <button type="submit" disabled={busy}>
            {busy ? 'Confirming…' : 'Confirm email'}
          </button>
        </form>
      )}

      {mode === 'signIn' && (
        <form onSubmit={handleSignIn}>
          <h2>Sign in</h2>
          <div>
            <label htmlFor="auth-username">Email address</label>
            <br />
            <input
              id="auth-username"
              type="email"
              autoComplete="email"
              required
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </div>
          <div>
            <label htmlFor="auth-password">Password</label>
            <br />
            <input
              id="auth-password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </div>
          <button type="submit" disabled={busy}>
            {busy ? 'Signing in…' : 'Sign in'}
          </button>
          <p>
            New here?{' '}
            <button type="button" onClick={() => switchMode('signUp')}>
              Create an account
            </button>
          </p>
        </form>
      )}

      {auth.error && (
        <p role="alert">
          {auth.error.message}
          {auth.error.action === 'signIn' && mode !== 'signIn' && (
            <>
              {' '}
              <button type="button" onClick={() => switchMode('signIn')}>
                Sign in instead
              </button>
            </>
          )}
        </p>
      )}

      <button type="button" onClick={onCancel}>
        Cancel
      </button>
    </section>
  )
}
