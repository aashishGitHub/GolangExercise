import Dexie, { type EntityTable } from 'dexie'

export interface CachedLayout {
  key: string // `${venueId}:${layoutVersion}`
  venueId: number
  layoutVersion: number
  meta: unknown // LayoutMeta, stored as plain JSON
  seatsBin: ArrayBuffer
  cachedAt: number
}

export const db = new Dexie('ticketing') as Dexie & {
  layouts: EntityTable<CachedLayout, 'key'>
}

db.version(1).stores({
  layouts: 'key, venueId, cachedAt',
})
