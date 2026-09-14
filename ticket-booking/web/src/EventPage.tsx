import { useCallback, useEffect, useState } from 'react'
import { getAvailability, getEvent, getPricing, type EventDetail, type PricingResponse } from './api/events'
import { bestAvailable, createHold, releaseHold, type Hold } from './api/holds'
import { loadSeatIndex } from './db/layoutCache'
import { HoldTimer } from './hold/HoldTimer'
import { SeatMapA11yTree } from './seatmap/a11y/SeatMapA11yTree'
import type { SeatIndex } from './seatmap/model/seatIndex'
import { SeatMapCanvas } from './seatmap/view/SeatMapCanvas'

interface Props {
  eventId: number
  idToken: () => Promise<string>
}

const MAX_SEATS = 8

export function EventPage({ eventId, idToken }: Props) {
  const [event, setEvent] = useState<EventDetail | null>(null)
  const [pricing, setPricing] = useState<PricingResponse | null>(null)
  const [index, setIndex] = useState<SeatIndex | null>(null)
  const [bitset, setBitset] = useState<Uint8Array | null>(null)
  const [selection, setSelection] = useState<number[]>([])
  const [focusOrdinal, setFocusOrdinal] = useState<number | null>(null)
  const [hold, setHold] = useState<Hold | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [liveMessage, setLiveMessage] = useState('')

  useEffect(() => {
    let cancelled = false
    async function load() {
      const ev = await getEvent(eventId)
      if (cancelled) return
      setEvent(ev)
      const [idx, avail, price] = await Promise.all([
        loadSeatIndex(ev.venueId, ev.layoutVersion, ev.layoutUrl),
        getAvailability(eventId),
        getPricing(eventId),
      ])
      if (cancelled) return
      setIndex(idx)
      setBitset(avail)
      setPricing(price)
    }
    load().catch((e) => setError(String(e)))
    return () => {
      cancelled = true
    }
  }, [eventId])

  // Availability polling until Phase 7's WS deltas land (docs/plan.md
  // Phase 4: "Map polls /availability until Phase 7").
  useEffect(() => {
    if (!event) return
    const id = setInterval(() => {
      getAvailability(eventId)
        .then(setBitset)
        .catch(() => {})
    }, 5000)
    return () => clearInterval(id)
  }, [event, eventId])

  const priceForTierIdx = pricing && index ? priceLookup(pricing, index.meta.tiers) : []

  const toggleSeat = useCallback(
    (ordinal: number) => {
      setSelection((sel) => {
        if (sel.includes(ordinal)) return sel.filter((o) => o !== ordinal)
        if (sel.length >= MAX_SEATS) return sel
        return [...sel, ordinal]
      })
    },
    [],
  )

  async function handleHold() {
    setError(null)
    try {
      const token = await idToken()
      const h = await createHold(eventId, selection, token)
      setHold(h)
      setLiveMessage(`Hold created for ${h.seats.length} seat${h.seats.length === 1 ? '' : 's'}.`)
    } catch (e) {
      setError(String(e))
    }
  }

  async function handleBestAvailable(quantity: number) {
    setError(null)
    try {
      const token = await idToken()
      const h = await bestAvailable(eventId, quantity, 100000, token)
      setHold(h)
      setSelection(h.seats.map((s) => s.seatOrdinal))
      setLiveMessage(`Best available: ${h.seats.length} seats assigned.`)
    } catch (e) {
      setError(String(e))
    }
  }

  async function handleRelease() {
    if (!hold) return
    const token = await idToken()
    await releaseHold(hold.holdId, token)
    setHold(null)
    setSelection([])
    setLiveMessage('Hold released.')
  }

  if (!event || !index || !bitset) {
    return <p>Loading event…</p>
  }

  return (
    <div>
      <h1>{event.title}</h1>
      <p>{event.artist}</p>

      {/* Best available is the first interactive element after the page
          heading, not buried after the map (docs/plan.md). */}
      <button onClick={() => handleBestAvailable(2)} disabled={!!hold}>
        Best available (2 seats)
      </button>

      <div aria-live="polite" className="visually-hidden">
        {liveMessage}
      </div>
      <div aria-live="assertive" className="visually-hidden">
        {error ?? ''}
      </div>

      <SeatMapCanvas index={index} bitset={bitset} selection={selection} focusOrdinal={focusOrdinal} onSelectSeat={toggleSeat} />
      <SeatMapA11yTree
        index={index}
        bitset={bitset}
        selection={selection}
        priceForTierIdx={priceForTierIdx}
        onSelectSeat={toggleSeat}
        onFocusOrdinal={setFocusOrdinal}
      />

      {selection.length > 0 && !hold && (
        <button onClick={handleHold}>Hold {selection.length} seat(s)</button>
      )}

      {hold && (
        <div>
          <HoldTimer expiresAt={hold.expiresAt} serverTime={hold.serverTime} />
          <p>Total: ${(hold.totalCents / 100).toFixed(2)}</p>
          <button onClick={handleRelease}>Release hold</button>
        </div>
      )}
    </div>
  )
}

/** Resolves seats.bin's tierIdx -> priceCents via layout.json's `tiers`
 * name array (tierIdx order) joined against the pricing endpoint's tiers
 * (name-keyed, a DIFFERENT — alphabetical — order). Matching by array
 * position between the two would be wrong; matching by name is correct. */
function priceLookup(pricing: PricingResponse, tierNamesByIdx: string[]): number[] {
  const priceByName = new Map(pricing.tiers.map((t) => [t.tierId, t.priceCents]))
  return tierNamesByIdx.map((name) => priceByName.get(name) ?? 0)
}
