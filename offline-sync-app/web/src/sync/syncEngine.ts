import { fetchAuthSession } from 'aws-amplify/auth'
import { db, type LocationRecord, type OutboxEntry, type SiteAssessmentRecord } from '../db/schema'

const backendUrl = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'

type Outcome = 'applied' | 'ignored_stale' | 'conflict' | 'limit_exceeded'
interface RecordResult {
  id: string
  outcome: Outcome
}
interface SyncResponse {
  locations: RecordResult[] | null
  siteAssessments: RecordResult[] | null
}

export interface DrainSummary {
  sent: number
  applied: number
  ignoredStale: number
  conflict: number
  limitExceeded: number
}

const EMPTY_SUMMARY: DrainSummary = { sent: 0, applied: 0, ignoredStale: 0, conflict: 0, limitExceeded: 0 }

function mergeSummary(a: DrainSummary, b: DrainSummary): DrainSummary {
  return {
    sent: a.sent + b.sent,
    applied: a.applied + b.applied,
    ignoredStale: a.ignoredStale + b.ignoredStale,
    conflict: a.conflict + b.conflict,
    limitExceeded: a.limitExceeded + b.limitExceeded,
  }
}

async function getIdToken(): Promise<string> {
  const session = await fetchAuthSession()
  const idToken = session.tokens?.idToken?.toString()
  if (!idToken) throw new Error('not authenticated — sign in before syncing')
  return idToken
}

// Drains every queued outbox entry. Locations/site-assessments go through one
// batched POST /api/sync (they carry no binary payload); photos go through
// the separate presign->PUT->confirm flow in drainPhotos, since a photo's
// image bytes have to reach S3 directly, never through this batch endpoint.
// Any outcome the server returns is terminal from the client's perspective —
// the server already made the LWW/idempotency call — so those entries are
// removed from the outbox regardless. Only a failed request (network down,
// 401, 5xx) leaves entries queued for the next retry/backoff attempt.
export async function drainOutbox(): Promise<DrainSummary> {
  const recordsSummary = await drainRecords()
  const photosSummary = await drainPhotos()
  return mergeSummary(recordsSummary, photosSummary)
}

async function drainRecords(): Promise<DrainSummary> {
  const entries = (await db.outbox.toArray()).filter(
    (e) => e.entityType === 'location' || e.entityType === 'siteAssessment',
  )
  if (entries.length === 0) return EMPTY_SUMMARY

  const locations = await resolveLocations(entries)
  const siteAssessments = await resolveSiteAssessments(entries)
  const idToken = await getIdToken()

  const res = await fetch(`${backendUrl}/api/sync`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${idToken}` },
    body: JSON.stringify({ locations, siteAssessments }),
  })
  if (!res.ok) {
    throw new Error(`sync failed: ${res.status} ${await res.text()}`)
  }
  const result: SyncResponse = await res.json()

  await db.outbox.bulkDelete(entries.map((e) => e.id!))

  return summarizeRecords(result)
}

async function drainPhotos(): Promise<DrainSummary> {
  const entries = (await db.outbox.toArray()).filter((e) => e.entityType === 'photo')
  if (entries.length === 0) return EMPTY_SUMMARY

  let summary = EMPTY_SUMMARY
  const idToken = await getIdToken()

  for (const entry of entries) {
    const photo = await db.photos.get(entry.entityId)
    const blobRow = await db.photoBlobs.get(entry.entityId)
    if (!photo || !blobRow) {
      // Nothing left to upload (already synced by an earlier attempt) — drop
      // the stale outbox entry rather than retrying forever.
      await db.outbox.delete(entry.id!)
      continue
    }

    const presignRes = await fetch(`${backendUrl}/api/sites/${photo.siteAssessmentId}/photos/presign`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${idToken}` },
      body: JSON.stringify({ photoId: photo.id, contentType: blobRow.blob.type }),
    })
    if (!presignRes.ok) throw new Error(`presign failed: ${presignRes.status} ${await presignRes.text()}`)
    const { uploadUrl, s3Key } = (await presignRes.json()) as { uploadUrl: string; s3Key: string }

    const putRes = await fetch(uploadUrl, {
      method: 'PUT',
      headers: { 'Content-Type': blobRow.blob.type },
      body: blobRow.blob,
    })
    if (!putRes.ok) throw new Error(`upload to storage failed: ${putRes.status}`)

    const confirmRes = await fetch(
      `${backendUrl}/api/sites/${photo.siteAssessmentId}/photos/${photo.id}/confirm`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${idToken}` },
        body: JSON.stringify({
          s3Key,
          latitude: photo.latitude,
          longitude: photo.longitude,
          condition: photo.condition,
          createdAt: photo.createdAt,
          updatedAt: photo.updatedAt,
        }),
      },
    )
    if (!confirmRes.ok) throw new Error(`confirm failed: ${confirmRes.status} ${await confirmRes.text()}`)
    const result: RecordResult = await confirmRes.json()

    if (result.outcome === 'applied') {
      await db.photos.update(photo.id, { s3Key })
      await db.photoBlobs.delete(photo.id)
    }
    await db.outbox.delete(entry.id!)

    summary = mergeSummary(summary, {
      sent: 1,
      applied: result.outcome === 'applied' ? 1 : 0,
      ignoredStale: result.outcome === 'ignored_stale' ? 1 : 0,
      conflict: result.outcome === 'conflict' ? 1 : 0,
      limitExceeded: result.outcome === 'limit_exceeded' ? 1 : 0,
    })
  }

  return summary
}

function idsFor(entries: OutboxEntry[], type: OutboxEntry['entityType']): string[] {
  return entries.filter((e) => e.entityType === type).map((e) => e.entityId)
}

async function resolveLocations(entries: OutboxEntry[]): Promise<LocationRecord[]> {
  const records = await Promise.all(idsFor(entries, 'location').map((id) => db.locations.get(id)))
  return records.filter((r): r is LocationRecord => r !== undefined)
}

async function resolveSiteAssessments(entries: OutboxEntry[]): Promise<SiteAssessmentRecord[]> {
  const records = await Promise.all(idsFor(entries, 'siteAssessment').map((id) => db.siteAssessments.get(id)))
  return records.filter((r): r is SiteAssessmentRecord => r !== undefined)
}

function summarizeRecords(result: SyncResponse): DrainSummary {
  const all = [...(result.locations ?? []), ...(result.siteAssessments ?? [])]
  return {
    sent: all.length,
    applied: all.filter((r) => r.outcome === 'applied').length,
    ignoredStale: all.filter((r) => r.outcome === 'ignored_stale').length,
    conflict: all.filter((r) => r.outcome === 'conflict').length,
    limitExceeded: all.filter((r) => r.outcome === 'limit_exceeded').length,
  }
}

const INITIAL_BACKOFF_MS = 2000
const MAX_BACKOFF_MS = 60_000

// Drives drainOutbox on a timer with exponential backoff on failure, reset on
// success or when the browser comes back online. Returns a stop function.
export function startAutoSync(onResult?: (summary: DrainSummary | Error) => void): () => void {
  let backoffMs = INITIAL_BACKOFF_MS
  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = false

  async function tick() {
    try {
      const summary = await drainOutbox()
      backoffMs = INITIAL_BACKOFF_MS
      onResult?.(summary)
    } catch (e) {
      backoffMs = Math.min(backoffMs * 2, MAX_BACKOFF_MS)
      onResult?.(e instanceof Error ? e : new Error(String(e)))
    } finally {
      if (!stopped) timer = setTimeout(tick, backoffMs)
    }
  }

  const onOnline = () => {
    backoffMs = INITIAL_BACKOFF_MS
    if (timer) clearTimeout(timer)
    tick()
  }
  window.addEventListener('online', onOnline)
  tick()

  return () => {
    stopped = true
    window.removeEventListener('online', onOnline)
    if (timer) clearTimeout(timer)
  }
}
