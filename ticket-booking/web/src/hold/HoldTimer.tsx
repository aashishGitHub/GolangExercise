import { useEffect, useRef, useState } from 'react'

interface Props {
  expiresAt: string // ISO, server time
  serverTime: string // ISO, server time at response
  /** Called once, the first tick remaining reaches zero — the UI's cue to
   * ask the server whether the hold is really gone rather than deciding so
   * itself (docs/plan.md: "the client never decides the hold is dead"). */
  onExpire?: () => void
}

const WARNING_THRESHOLD_SECONDS = 120

/** Server-authoritative countdown (docs/plan.md "Hold timer & checkout") —
 * remaining is always RECOMPUTED from expiresAt via the server/client clock
 * skew, never decremented locally. Announces at coarse intervals only
 * (5:00/2:00/1:00/0:30/0:10), never every second. */
export function HoldTimer({ expiresAt, serverTime, onExpire }: Props) {
  const skewMs = new Date(serverTime).getTime() - Date.now()
  const [remaining, setRemaining] = useState(secondsUntil(expiresAt, skewMs))
  const firedExpireRef = useRef(false)

  useEffect(() => {
    firedExpireRef.current = false
  }, [expiresAt])

  useEffect(() => {
    const id = setInterval(() => setRemaining(secondsUntil(expiresAt, skewMs)), 1000)
    return () => clearInterval(id)
  }, [expiresAt, skewMs])

  useEffect(() => {
    if (remaining <= 0 && !firedExpireRef.current) {
      firedExpireRef.current = true
      onExpire?.()
    }
  }, [remaining, onExpire])

  const mm = Math.floor(Math.max(0, remaining) / 60)
  const ss = Math.max(0, remaining) % 60
  const warning = remaining > 0 && remaining <= WARNING_THRESHOLD_SECONDS

  return (
    <div role="timer" aria-label={`Time left to complete your purchase: ${mm} minutes ${ss} seconds${warning ? ', hurry' : ''}`}>
      Time left to complete your purchase: {String(mm).padStart(2, '0')}:{String(ss).padStart(2, '0')}
      {warning && ' — hurry, your seats will be released soon'}
    </div>
  )
}

function secondsUntil(expiresAt: string, skewMs: number): number {
  const now = Date.now() + skewMs
  return Math.round((new Date(expiresAt).getTime() - now) / 1000)
}
