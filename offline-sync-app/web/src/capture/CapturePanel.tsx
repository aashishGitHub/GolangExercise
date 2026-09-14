import { useState } from 'react'
import { useLiveQuery } from 'dexie-react-hooks'
import { db, type Condition } from '../db/schema'
import { captureLocation, capturePhoto, captureSiteAssessment } from './capture'
import { drainOutbox, type DrainSummary } from '../sync/syncEngine'

// Proves the offline-first loop: capture works with zero network/auth
// (writes straight to Dexie + enqueues the outbox), sync only runs on demand
// here and requires a signed-in session — "auth gates sync, not capture".
export function CapturePanel({ onLocationCaptured }: { onLocationCaptured?: (id: string) => void }) {
  const [locationId, setLocationId] = useState<string | null>(null)
  const [siteId, setSiteId] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [lat, setLat] = useState('1.0')
  const [lng, setLng] = useState('2.0')
  const [siteName, setSiteName] = useState('')
  const [condition, setCondition] = useState<Condition>('good')
  const [summary, setSummary] = useState<DrainSummary | null>(null)
  const [error, setError] = useState('')

  const outboxCount = useLiveQuery(() => db.outbox.count(), []) ?? 0
  const locations = useLiveQuery(() => db.locations.toArray(), []) ?? []
  const photoCount = useLiveQuery(() => db.photos.count(), []) ?? 0

  async function handleCaptureLocation() {
    const record = await captureLocation({ name, latitude: Number(lat), longitude: Number(lng) })
    setLocationId(record.id)
    onLocationCaptured?.(record.id)
    setName('')
  }

  async function handleCaptureSite() {
    if (!locationId) return
    const record = await captureSiteAssessment({ locationId, name: siteName, notes: '' })
    setSiteId(record.id)
    setSiteName('')
  }

  async function handleCapturePhoto(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file || !siteId) return
    await capturePhoto({ siteAssessmentId: siteId, file, latitude: Number(lat), longitude: Number(lng), condition })
  }

  async function handleSyncNow() {
    setError('')
    try {
      const s = await drainOutbox()
      setSummary(s)
    } catch (e) {
      setError(String(e))
    }
  }

  return (
    <section style={{ maxWidth: 360, margin: '2rem auto', fontFamily: 'sans-serif' }}>
      <h2>Capture (offline check)</h2>
      <p>
        outbox: {outboxCount} pending · locations: {locations.length} · photos: {photoCount}
      </p>

      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '0.5rem' }}>
        <input placeholder="location name" value={name} onChange={(e) => setName(e.target.value)} />
        <input placeholder="lat" value={lat} onChange={(e) => setLat(e.target.value)} style={{ width: 60 }} />
        <input placeholder="lng" value={lng} onChange={(e) => setLng(e.target.value)} style={{ width: 60 }} />
        <button onClick={handleCaptureLocation}>Capture location</button>
      </div>

      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '0.5rem' }}>
        <input placeholder="site name" value={siteName} onChange={(e) => setSiteName(e.target.value)} />
        <button onClick={handleCaptureSite} disabled={!locationId}>
          Capture site (for last location)
        </button>
      </div>

      <div style={{ display: 'flex', gap: '0.5rem', marginBottom: '0.5rem', alignItems: 'center' }}>
        <select value={condition} onChange={(e) => setCondition(e.target.value as Condition)}>
          <option value="good">good</option>
          <option value="moderate">moderate</option>
          <option value="bad">bad</option>
        </select>
        <label>
          Capture photo (for last site)
          <input type="file" accept="image/*" onChange={handleCapturePhoto} disabled={!siteId} />
        </label>
      </div>

      <button onClick={handleSyncNow}>Sync now</button>

      {summary && (
        <p>
          sync: sent {summary.sent}, applied {summary.applied}, stale {summary.ignoredStale}, conflict{' '}
          {summary.conflict}, limitExceeded {summary.limitExceeded}
        </p>
      )}
      {error && <p style={{ color: 'crimson' }}>error: {error}</p>}
    </section>
  )
}
