import { useState } from 'react'
import {
  confirmSignUp,
  fetchAuthSession,
  getCurrentUser,
  signIn,
  signOut,
  signUp,
} from 'aws-amplify/auth'

type Mode = 'signUp' | 'confirm' | 'signIn'

// Proves the Amplify <-> cognito-local <-> Go JWT-middleware loop end to end.
// Real capture/dashboard UI lands in later phases; this is Phase 1's artifact.
// cognito-local doesn't support SRP, so sign-in must request USER_PASSWORD_AUTH
// explicitly (Amplify defaults to USER_SRP_AUTH against real Cognito).
export function AuthPanel() {
  const [mode, setMode] = useState<Mode>('signUp')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [whoami, setWhoami] = useState<string | null>(null)
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')

  const backendUrl = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'

  async function handleSignUp() {
    setError('')
    try {
      const result = await signUp({
        username: email,
        password,
        options: { userAttributes: { email } },
      })
      setStatus(`signUp: ${result.nextStep.signUpStep}`)
      setMode('confirm')
    } catch (e) {
      setError(String(e))
    }
  }

  async function handleConfirm() {
    setError('')
    try {
      await confirmSignUp({ username: email, confirmationCode: code })
      setStatus('confirmed — you can sign in now')
      setMode('signIn')
    } catch (e) {
      setError(String(e))
    }
  }

  async function handleSignIn() {
    setError('')
    try {
      await signIn({
        username: email,
        password,
        options: { authFlowType: 'USER_PASSWORD_AUTH' },
      })
      const user = await getCurrentUser()
      setStatus(`signed in as ${user.username}`)
    } catch (e) {
      setError(String(e))
    }
  }

  async function handleSignOut() {
    await signOut()
    setStatus('signed out')
    setWhoami(null)
  }

  async function handleCallWhoami() {
    setError('')
    try {
      const session = await fetchAuthSession()
      const idToken = session.tokens?.idToken?.toString()
      if (!idToken) throw new Error('no id token in session — sign in first')

      const res = await fetch(`${backendUrl}/api/whoami`, {
        headers: { Authorization: `Bearer ${idToken}` },
      })
      setWhoami(`${res.status} ${await res.text()}`)
    } catch (e) {
      setError(String(e))
    }
  }

  return (
    <section style={{ maxWidth: 360, margin: '2rem auto', fontFamily: 'sans-serif' }}>
      <h2>Auth (Phase 1 check)</h2>

      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '1rem' }}>
        <button onClick={() => setMode('signUp')} disabled={mode === 'signUp'}>Register</button>
        <button onClick={() => setMode('confirm')} disabled={mode === 'confirm'}>Confirm</button>
        <button onClick={() => setMode('signIn')} disabled={mode === 'signIn'}>Login</button>
      </div>

      {mode !== 'confirm' && (
        <>
          <input placeholder="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          <input
            placeholder="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </>
      )}
      {mode === 'confirm' && (
        <>
          <input placeholder="email" value={email} onChange={(e) => setEmail(e.target.value)} />
          <input placeholder="confirmation code" value={code} onChange={(e) => setCode(e.target.value)} />
        </>
      )}

      <div style={{ marginTop: '0.5rem', display: 'flex', gap: '0.5rem' }}>
        {mode === 'signUp' && <button onClick={handleSignUp}>Register</button>}
        {mode === 'confirm' && <button onClick={handleConfirm}>Confirm</button>}
        {mode === 'signIn' && <button onClick={handleSignIn}>Login</button>}
        <button onClick={handleSignOut}>Sign out</button>
        <button onClick={handleCallWhoami}>Call /api/whoami</button>
      </div>

      {status && <p>status: {status}</p>}
      {whoami && <p>whoami: {whoami}</p>}
      {error && <p style={{ color: 'crimson' }}>error: {error}</p>}
    </section>
  )
}
