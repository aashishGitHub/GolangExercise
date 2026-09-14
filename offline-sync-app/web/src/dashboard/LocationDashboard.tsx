import { useState } from 'react'
import { useLiveQuery } from 'dexie-react-hooks'
import { db, type Condition } from '../db/schema'
import { pullLocation } from '../sync/pull'

// Part-to-whole of an ordered severity scale is a job for a stacked bar, not
// a pie (dataviz skill's form table has no "pie" row; part-to-whole -> stacked
// bar). Colors are the reserved status palette (good/warning/critical), never
// reused as generic categorical hues, always paired with a text legend so
// identity never rides on color alone (warning's ~1.8:1 contrast on light
// surfaces is a known, documented tradeoff mitigated by that label).
const STATUS_COLORS: Record<Condition, string> = {
  good: '#0ca30c',
  moderate: '#fab219',
  bad: '#d03b3b',
}
const ORDER: Condition[] = ['good', 'moderate', 'bad']

interface ConditionCounts {
  good: number
  moderate: number
  bad: number
}

export function LocationDashboard({ locationId }: { locationId: string }) {
  const [pulling, setPulling] = useState(false)
  const [pullError, setPullError] = useState('')

  const counts =
    useLiveQuery<ConditionCounts>(async () => {
      const sites = await db.siteAssessments.where('locationId').equals(locationId).toArray()
      const siteIds = sites.map((s) => s.id)
      const photos = siteIds.length ? await db.photos.where('siteAssessmentId').anyOf(siteIds).toArray() : []
      return {
        good: photos.filter((p) => p.condition === 'good').length,
        moderate: photos.filter((p) => p.condition === 'moderate').length,
        bad: photos.filter((p) => p.condition === 'bad').length,
      }
    }, [locationId]) ?? { good: 0, moderate: 0, bad: 0 }

  const total = counts.good + counts.moderate + counts.bad

  async function handlePull() {
    setPulling(true)
    setPullError('')
    try {
      await pullLocation(locationId)
    } catch (e) {
      setPullError(String(e))
    } finally {
      setPulling(false)
    }
  }

  return (
    <section style={{ maxWidth: 360, margin: '2rem auto', fontFamily: 'sans-serif' }}>
      <h2>Dashboard (condition breakdown)</h2>
      <button onClick={handlePull} disabled={pulling}>
        {pulling ? 'Pulling…' : 'Pull latest from server'}
      </button>
      {pullError && <p style={{ color: 'crimson' }}>error: {pullError}</p>}

      {total === 0 ? (
        <p>no photos synced for this location yet</p>
      ) : (
        <>
          <div
            role="img"
            aria-label={`Condition breakdown: ${ORDER.map((k) => `${k} ${counts[k]}`).join(', ')}`}
            style={{ display: 'flex', height: 24, borderRadius: 4, overflow: 'hidden', gap: 2, marginTop: '0.75rem' }}
          >
            {ORDER.filter((k) => counts[k] > 0).map((k) => (
              <div
                key={k}
                title={`${k}: ${counts[k]}`}
                style={{ width: `${(counts[k] / total) * 100}%`, background: STATUS_COLORS[k] }}
              />
            ))}
          </div>

          <ul style={{ listStyle: 'none', padding: 0, display: 'flex', gap: '1rem', marginTop: '0.5rem' }}>
            {ORDER.map((k) => (
              <li key={k} style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                <span
                  aria-hidden="true"
                  style={{ width: 10, height: 10, borderRadius: 2, background: STATUS_COLORS[k], display: 'inline-block' }}
                />
                {k}: {counts[k]}
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  )
}
