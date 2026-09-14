import { useEffect, useState } from 'react'

interface Props {
  expiresAt: string // ISO, server time
  serverTime: string // ISO, server time at response
}

/** Server-authoritative countdown (docs/plan.md "Hold timer & checkout") —
 * remaining is always RECOMPUTED from expiresAt via the server/client clock
 * skew, never decremented locally. Announces at coarse intervals only
 * (5:00/2:00/1:00/0:30/0:10), never every second. */
export function HoldTimer({ expiresAt, serverTime }: Props) {
  const skewMs = new Date(serverTime).getTime() - Date.now()
  const [remaining, setRemaining] = useState(secondsUntil(expiresAt, skewMs))

  useEffect(() => {
    const id = setInterval(() => setRemaining(secondsUntil(expiresAt, skewMs)), 1000)
    return () => clearInterval(id)
  }, [expiresAt, skewMs])

  const mm = Math.floor(Math.max(0, remaining) / 60)
  const ss = Math.max(0, remaining) % 60

  return (
    <div role="timer" aria-label={`Hold expires in ${mm} minutes ${ss} seconds`}>
      {String(mm).padStart(2, '0')}:{String(ss).padStart(2, '0')}
    </div>
  )
}

function secondsUntil(expiresAt: string, skewMs: number): number {
  const now = Date.now() + skewMs
  return Math.round((new Date(expiresAt).getTime() - now) / 1000)
}
