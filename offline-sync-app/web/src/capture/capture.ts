import { db, type Condition, type LocationRecord, type PhotoRecord, type SiteAssessmentRecord } from '../db/schema'
import { watermarkImage } from './watermark'

// Capture never touches the network or checks auth — "auth gates sync, not
// capture": a field worker with no signal must be able to record data.
// Entity write + outbox enqueue happen in one Dexie transaction so a crash
// between them can't silently drop a record from the sync queue.

export async function captureLocation(input: {
  name: string
  latitude: number
  longitude: number
}): Promise<LocationRecord> {
  const now = new Date().toISOString()
  const record: LocationRecord = {
    id: crypto.randomUUID(),
    name: input.name,
    latitude: input.latitude,
    longitude: input.longitude,
    createdAt: now,
    updatedAt: now,
  }

  await db.transaction('rw', db.locations, db.outbox, async () => {
    await db.locations.add(record)
    await db.outbox.add({ entityType: 'location', entityId: record.id, queuedAt: now })
  })

  return record
}

export async function captureSiteAssessment(input: {
  locationId: string
  name: string
  notes: string
}): Promise<SiteAssessmentRecord> {
  const now = new Date().toISOString()
  const record: SiteAssessmentRecord = {
    id: crypto.randomUUID(),
    locationId: input.locationId,
    name: input.name,
    notes: input.notes,
    createdAt: now,
    updatedAt: now,
  }

  await db.transaction('rw', db.siteAssessments, db.outbox, async () => {
    await db.siteAssessments.add(record)
    await db.outbox.add({ entityType: 'siteAssessment', entityId: record.id, queuedAt: now })
  })

  return record
}

export async function capturePhoto(input: {
  siteAssessmentId: string
  file: File
  latitude: number
  longitude: number
  condition: Condition
}): Promise<PhotoRecord> {
  const now = new Date().toISOString()
  const id = crypto.randomUUID()
  const label = `${input.latitude.toFixed(5)}, ${input.longitude.toFixed(5)} · ${now}`
  const blob = await watermarkImage(input.file, label)

  // s3Key is empty until the sync engine's presign->PUT->confirm flow lands
  // it in storage; photoBlobs holds the pending bytes until then.
  const record: PhotoRecord = {
    id,
    siteAssessmentId: input.siteAssessmentId,
    s3Key: '',
    latitude: input.latitude,
    longitude: input.longitude,
    condition: input.condition,
    createdAt: now,
    updatedAt: now,
  }

  await db.transaction('rw', db.photos, db.photoBlobs, db.outbox, async () => {
    await db.photos.add(record)
    await db.photoBlobs.add({ photoId: id, blob })
    await db.outbox.add({ entityType: 'photo', entityId: id, queuedAt: now })
  })

  return record
}
