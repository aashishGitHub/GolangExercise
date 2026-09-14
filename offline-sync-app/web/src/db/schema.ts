import Dexie, { type Table } from 'dexie'

// Mirrors the server's sync.Request wire shape (fieldsync/internal/sync) so
// outbox entries can be posted to /api/sync without translation.
export interface LocationRecord {
  id: string
  name: string
  latitude: number
  longitude: number
  createdAt: string
  updatedAt: string
}

export interface SiteAssessmentRecord {
  id: string
  locationId: string
  name: string
  notes: string
  createdAt: string
  updatedAt: string
}

export type Condition = 'good' | 'moderate' | 'bad'

export interface PhotoRecord {
  id: string
  siteAssessmentId: string
  s3Key: string
  latitude: number
  longitude: number
  condition: Condition
  createdAt: string
  updatedAt: string
}

export type EntityType = 'location' | 'siteAssessment' | 'photo'

// One row per record that still needs to reach the server. Capture writes to
// the entity table AND enqueues here in the same Dexie transaction, so a
// crash between the two can't happen — either both land or neither does.
export interface OutboxEntry {
  id?: number
  entityType: EntityType
  entityId: string
  queuedAt: string
}

// The not-yet-uploaded watermarked image bytes for a photo. Kept out of the
// `photos` table (which mirrors the server's metadata-only schema) so
// listing/serializing photo metadata never has to load image bytes into
// memory. Deleted once the presign->PUT->confirm flow succeeds.
export interface PhotoBlobRecord {
  photoId: string
  blob: Blob
}

class FieldSyncDB extends Dexie {
  locations!: Table<LocationRecord, string>
  siteAssessments!: Table<SiteAssessmentRecord, string>
  photos!: Table<PhotoRecord, string>
  photoBlobs!: Table<PhotoBlobRecord, string>
  outbox!: Table<OutboxEntry, number>

  constructor() {
    super('fieldsync')
    this.version(1).stores({
      locations: 'id, updatedAt',
      siteAssessments: 'id, locationId, updatedAt',
      photos: 'id, siteAssessmentId, updatedAt',
      photoBlobs: 'photoId',
      outbox: '++id, entityType, entityId',
    })
  }
}

export const db = new FieldSyncDB()
