import { useState } from 'react'
import './amplifyConfig'
import { useAuth } from './auth/useAuth'
import { EventPage } from './EventPage'

const CODE = '123456' // cognito-local's fixed confirmation code

export function App() {
  const auth = useAuth()
  const [mode, setMode] = useState<'signUp' | 'confirm' | 'signIn'>('signUp')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState(CODE)
  const [signedIn, setSignedIn] = useState(false)

  if (!signedIn) {
    return (
      <div>
        <h1>Ticketing</h1>
        {mode === 'signUp' && (
          <form
            onSubmit={async (e) => {
              e.preventDefault()
              await auth.register(username, password)
              setMode('confirm')
            }}
          >
            <input aria-label="email" value={username} onChange={(e) => setUsername(e.target.value)} />
            <input aria-label="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
            <button type="submit">Sign up</button>
          </form>
        )}
        {mode === 'confirm' && (
          <form
            onSubmit={async (e) => {
              e.preventDefault()
              await auth.confirm(username, code)
              setMode('signIn')
            }}
          >
            <input aria-label="confirmation code" value={code} onChange={(e) => setCode(e.target.value)} />
            <button type="submit">Confirm</button>
          </form>
        )}
        {mode === 'signIn' && (
          <form
            onSubmit={async (e) => {
              e.preventDefault()
              await auth.login(username, password)
              setSignedIn(true)
            }}
          >
            <input aria-label="email" value={username} onChange={(e) => setUsername(e.target.value)} />
            <input aria-label="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
            <button type="submit">Log in</button>
          </form>
        )}
        {auth.error && <p role="alert">{auth.error}</p>}
      </div>
    )
  }

  return <EventPage eventId={1} idToken={auth.idToken} />
}
