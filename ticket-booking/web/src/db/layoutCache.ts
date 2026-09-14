import { fetchLayoutMeta, fetchSeatsBin } from '../api/events'
import { buildSeatIndex, type LayoutMeta, type SeatIndex } from '../seatmap/model/seatIndex'
import { db } from './schema'

const MAX_CACHED_VENUES = 3

/** Cache-first layout load — docs/plan.md "the static layout in IndexedDB,
 * so a returning user renders the map instantly". A network round trip
 * only happens on a cache miss (first visit, or a new layoutVersion). */
export async function loadSeatIndex(venueId: number, layoutVersion: number, layoutUrl: string): Promise<SeatIndex> {
  const key = `${venueId}:${layoutVersion}`
  const cached = await db.layouts.get(key)
  if (cached) {
    return buildSeatIndex(cached.meta as LayoutMeta, cached.seatsBin)
  }

  const [meta, seatsBin] = await Promise.all([fetchLayoutMeta(layoutUrl), fetchSeatsBin(layoutUrl)])
  const index = buildSeatIndex(meta, seatsBin)

  await db.layouts.put({ key, venueId, layoutVersion, meta, seatsBin, cachedAt: Date.now() })
  await evictOldVenues(venueId)

  return index
}

/** LRU-cap at MAX_CACHED_VENUES distinct venues (docs/plan.md "Static
 * layout format" — "LRU-capped at 3 venues"). Old *versions* of the SAME
 * venue are also dropped — a stale layoutVersion should never linger once
 * a fresher one has been cached. */
async function evictOldVenues(justCachedVenueId: number): Promise<void> {
  const all = await db.layouts.toArray()

  // Drop older versions of the venue we just cached.
  const sameVenue = all.filter((l) => l.venueId === justCachedVenueId).sort((a, b) => b.layoutVersion - a.layoutVersion)
  for (const stale of sameVenue.slice(1)) {
    await db.layouts.delete(stale.key)
  }

  // LRU-cap distinct venues.
  const remaining = await db.layouts.toArray()
  const venueIds = [...new Set(remaining.map((l) => l.venueId))]
  if (venueIds.length <= MAX_CACHED_VENUES) return

  const byVenueLatestCachedAt = new Map<number, number>()
  for (const l of remaining) {
    const cur = byVenueLatestCachedAt.get(l.venueId) ?? 0
    if (l.cachedAt > cur) byVenueLatestCachedAt.set(l.venueId, l.cachedAt)
  }
  const oldestFirst = [...byVenueLatestCachedAt.entries()].sort((a, b) => a[1] - b[1])
  const toEvict = oldestFirst.slice(0, venueIds.length - MAX_CACHED_VENUES).map(([id]) => id)
  for (const venueId of toEvict) {
    await db.layouts.where('venueId').equals(venueId).delete()
  }
}
