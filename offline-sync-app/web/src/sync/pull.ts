import { fetchAuthSession } from 'aws-amplify/auth'
import { db } from '../db/schema'

const backendUrl = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'

interface ServerLocation {
  id: string
  name: string
  latitude: number
  longitude: number
  createdAt: string
  updatedAt: string
  siteAssessments: ServerSiteAssessment[] | null
}
interface ServerSiteAssessment {
  id: string
  locationId: string
  name: string
  notes: string
  createdAt: string
  updatedAt: string
}
interface ServerSite {
  id: string
  locationId: string
  name: string
  notes: string
  createdAt: string
  updatedAt: string
  photos: ServerPhoto[] | null
}
interface ServerPhoto {
  id: string
  siteAssessmentId: string
  s3Key: string
  latitude: number
  longitude: number
  condition: 'good' | 'moderate' | 'bad'
  createdAt: string
  updatedAt: string
}

// Pulls one location (its sites + their photos) from the server into local
// Dexie tables — "pull-on-login" per the plan doc, so the dashboard reflects
// what other field workers/devices have synced, not just this device's own
// captures. A plain `put` (last writer client-side wins locally) is enough
// here: the server has already resolved the authoritative LWW state, this
// just mirrors it — it does not need to re-run conflict resolution.
export async function pullLocation(locationId: string): Promise<void> {
  const session = await fetchAuthSession()
  const idToken = session.tokens?.idToken?.toString()
  if (!idToken) throw new Error('not authenticated — sign in before pulling')

  const headers = { Authorization: `Bearer ${idToken}` }

  const locRes = await fetch(`${backendUrl}/api/locations/${locationId}`, { headers })
  if (!locRes.ok) throw new Error(`pull location failed: ${locRes.status}`)
  const location: ServerLocation = await locRes.json()

  await db.locations.put({
    id: location.id,
    name: location.name,
    latitude: location.latitude,
    longitude: location.longitude,
    createdAt: location.createdAt,
    updatedAt: location.updatedAt,
  })

  for (const site of location.siteAssessments ?? []) {
    const siteRes = await fetch(`${backendUrl}/api/sites/${site.id}`, { headers })
    if (!siteRes.ok) throw new Error(`pull site failed: ${siteRes.status}`)
    const fullSite: ServerSite = await siteRes.json()

    await db.siteAssessments.put({
      id: fullSite.id,
      locationId: fullSite.locationId,
      name: fullSite.name,
      notes: fullSite.notes,
      createdAt: fullSite.createdAt,
      updatedAt: fullSite.updatedAt,
    })

    await db.photos.bulkPut(
      (fullSite.photos ?? []).map((p) => ({
        id: p.id,
        siteAssessmentId: p.siteAssessmentId,
        s3Key: p.s3Key,
        latitude: p.latitude,
        longitude: p.longitude,
        condition: p.condition,
        createdAt: p.createdAt,
        updatedAt: p.updatedAt,
      })),
    )
  }
}
