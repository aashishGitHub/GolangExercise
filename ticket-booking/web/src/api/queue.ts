import { apiFetch } from './client'

export type QueueJoinResult =
  | { admitted: true; admissionToken: string }
  | { admitted: false; position: number; etaSeconds: number; pollAfterMs: number }

interface QueueJoinRaw {
  admissionToken?: string
  position?: number
  etaSeconds?: number
  pollAfterMs?: number
}

/** POST /events/{id}/queue — 200 with an admissionToken once past the
 * cursor, 202 with a position while still queued (internal/httpapi/
 * waitingroom.go). Every write to .../holds requires the resulting token
 * in X-Admission-Token — this is not optional even at low load. */
export async function joinQueue(eventId: number, token: string): Promise<QueueJoinResult> {
  const raw = await apiFetch<QueueJoinRaw>(`/api/v1/events/${eventId}/queue`, { method: 'POST', token })
  if (raw.admissionToken) return { admitted: true, admissionToken: raw.admissionToken }
  return { admitted: false, position: raw.position ?? 0, etaSeconds: raw.etaSeconds ?? 0, pollAfterMs: raw.pollAfterMs ?? 2000 }
}

const MAX_QUEUE_WAIT_MS = 30_000

/** Joins the queue and waits for admission, polling at the server's own
 * pollAfterMs cadence. Bounded to MAX_QUEUE_WAIT_MS — this app doesn't
 * (yet) have a waiting-room screen showing live position, so a visitor
 * stuck behind a real queue longer than that gets a clear error instead
 * of an indefinite spinner (docs/plan.md Phase 13: waiting-room UI is a
 * documented, separate gap). At realistic local load this resolves on the
 * first call. */
export async function waitForAdmission(eventId: number, token: string): Promise<string> {
  const deadline = Date.now() + MAX_QUEUE_WAIT_MS
  for (;;) {
    const result = await joinQueue(eventId, token)
    if (result.admitted) return result.admissionToken
    if (Date.now() >= deadline) {
      throw new Error('Still waiting for the queue — please try again shortly.')
    }
    await new Promise((resolve) => setTimeout(resolve, result.pollAfterMs))
  }
}
