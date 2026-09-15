import { useCallback, useEffect, useState } from 'react'
import { describeError } from './api/errorCopy'
import { getAvailability, getEvent, getPricing, type EventDetail, type PricingResponse } from './api/events'
import { bestAvailable, createHold, getHold, releaseHold, type Hold } from './api/holds'
import { waitForAdmission } from './api/queue'
import { loadSeatIndex } from './db/layoutCache'
import { HoldTimer } from './hold/HoldTimer'
import { SeatMapA11yTree } from './seatmap/a11y/SeatMapA11yTree'
import { getState, STATE_FREE } from './seatmap/model/bitset'
import { rowOfOrdinal, sectionOfOrdinal, type SeatIndex } from './seatmap/model/seatIndex'
import { SeatMapCanvas } from './seatmap/view/SeatMapCanvas'

interface Props {
  eventId: number
  idToken: () => Promise<string>
  isAuthed: boolean
  /** Called when a reserve action needs a signed-in user. `retry` re-runs
   * the exact action the visitor originally requested once sign-in
   * completes — the visitor never has to reselect their seats. */
  onAuthRequired: (retry: () => void) => void
}

const MAX_SEATS = 8
// Effectively "no cap" for the demo catalog: higher than any seeded tier,
// used only because the API requires a maxPriceCents on a best-available
// request that doesn't otherwise constrain by price.
const NO_PRACTICAL_PRICE_CAP_CENTS = 100_000_00

type LoadStatus = 'loading' | 'ready' | 'error'

export function EventPage({ eventId, idToken, isAuthed, onAuthRequired }: Props) {
  const [status, setStatus] = useState<LoadStatus>('loading')
  const [loadErrorMessage, setLoadErrorMessage] = useState('')
  const [event, setEvent] = useState<EventDetail | null>(null)
  const [pricing, setPricing] = useState<PricingResponse | null>(null)
  const [index, setIndex] = useState<SeatIndex | null>(null)
  const [bitset, setBitset] = useState<Uint8Array | null>(null)
  const [selection, setSelection] = useState<number[]>([])
  const [focusOrdinal, setFocusOrdinal] = useState<number | null>(null)
  const [hold, setHold] = useState<Hold | null>(null)
  const [bestAvailableQty, setBestAvailableQty] = useState(2)
  const [actionError, setActionError] = useState<string | null>(null)
  const [liveMessage, setLiveMessage] = useState('')

  const load = useCallback(async () => {
    setStatus('loading')
    try {
      const ev = await getEvent(eventId)
      const [idx, avail, price] = await Promise.all([
        loadSeatIndex(ev.venueId, ev.layoutVersion, ev.layoutUrl),
        getAvailability(eventId),
        getPricing(eventId),
      ])
      setEvent(ev)
      setIndex(idx)
      setBitset(avail)
      setPricing(price)
      setStatus('ready')
    } catch (e) {
      setLoadErrorMessage(describeError(e))
      setStatus('error')
    }
  }, [eventId])

  useEffect(() => {
    void load()
  }, [load])

  // Availability polling until Phase 7's WS deltas land (docs/plan.md
  // Phase 4: "Map polls /availability until Phase 7"). Each poll also
  // reconciles the current selection: a seat someone else just booked is
  // dropped from selection and announced, instead of only surfacing as a
  // 409 when the visitor tries to reserve.
  useEffect(() => {
    if (status !== 'ready') return
    const id = setInterval(() => {
      getAvailability(eventId)
        .then((next) => {
          setBitset(next)
          setSelection((sel) => {
            const stillFree = sel.filter((o) => getState(next, o) === STATE_FREE)
            if (stillFree.length < sel.length && index) {
              const taken = sel.filter((o) => !stillFree.includes(o))
              setLiveMessage(
                `${taken.length === 1 ? 'A seat' : `${taken.length} seats`} you had selected — ` +
                  `${taken.map((o) => shortSeatLabel(index, o)).join(', ')} — ${taken.length === 1 ? 'was' : 'were'} just taken and removed from your selection.`,
              )
            }
            return stillFree
          })
        })
        .catch(() => {})
    }, 5000)
    return () => clearInterval(id)
  }, [status, eventId, index])

  const priceForTierIdx = pricing && index ? priceLookup(pricing, index.meta.tiers) : []

  const toggleSeat = useCallback(
    (ordinal: number) => {
      setSelection((sel) => {
        if (sel.includes(ordinal)) return sel.filter((o) => o !== ordinal)
        if (sel.length >= MAX_SEATS) {
          setLiveMessage(`You can select up to ${MAX_SEATS} seats at a time. Deselect one to choose another.`)
          return sel
        }
        return [...sel, ordinal]
      })
    },
    [],
  )

  // Split from the auth check on purpose: a retry callback handed to
  // onAuthRequired must not re-check `isAuthed` when it finally runs,
  // because that would close over the stale (false) value from the render
  // where the visitor first clicked "Reserve" — it would never see the
  // sign-in that just happened. These call idToken() directly instead,
  // which reads the live session fresh on every invocation.
  async function reserveSeats(seatOrdinals: number[]) {
    setActionError(null)
    try {
      const token = await idToken()
      // Every hold is gated behind the waiting room, unconditionally
      // (internal/waitingroom/middleware.go) — admission is requested
      // fresh for each attempt rather than cached, since a token is
      // single-use-scoped and short-lived.
      const admissionToken = await waitForAdmission(eventId, token)
      const h = await createHold(eventId, seatOrdinals, token, admissionToken)
      setHold(h)
      setLiveMessage(`Reserved ${h.seats.length} seat${h.seats.length === 1 ? '' : 's'}. Complete checkout before the timer runs out.`)
    } catch (e) {
      setActionError(describeError(e))
    }
  }

  async function reserveBestAvailable(quantity: number) {
    setActionError(null)
    try {
      const token = await idToken()
      const admissionToken = await waitForAdmission(eventId, token)
      const h = await bestAvailable(eventId, quantity, NO_PRACTICAL_PRICE_CAP_CENTS, token, admissionToken)
      setHold(h)
      setSelection(h.seats.map((s) => s.seatOrdinal))
      setLiveMessage(`Best available: ${h.seats.length} seat${h.seats.length === 1 ? '' : 's'} reserved for you.`)
    } catch (e) {
      setActionError(describeError(e))
    }
  }

  function handleHold() {
    if (!isAuthed) {
      onAuthRequired(() => void reserveSeats(selection))
      return
    }
    void reserveSeats(selection)
  }

  function handleBestAvailable(quantity: number) {
    if (!isAuthed) {
      onAuthRequired(() => void reserveBestAvailable(quantity))
      return
    }
    void reserveBestAvailable(quantity)
  }

  async function handleRelease() {
    if (!hold) return
    try {
      const token = await idToken()
      await releaseHold(hold.holdId, token)
    } catch (e) {
      setActionError(describeError(e))
      return
    }
    setHold(null)
    setSelection([])
    setLiveMessage('Reservation released. Those seats are available again.')
  }

  // "The client never decides the hold is dead — at zero it asks the
  // server" (docs/plan.md). Called once by HoldTimer when its countdown
  // reaches zero.
  const handleHoldExpire = useCallback(async () => {
    if (!hold) return
    try {
      const token = await idToken()
      const current = await getHold(hold.holdId, token)
      if (current.secondsRemaining > 0) return // clock skew false alarm — still live
    } catch {
      // Expected: the server already reclaimed it (410/404-shaped error).
    }
    setHold(null)
    setSelection([])
    setLiveMessage('Your reservation time ran out and those seats were released. Please choose again.')
  }, [hold, idToken])

  if (status === 'loading') {
    return <p>Loading seat map…</p>
  }

  if (status === 'error') {
    return (
      <div>
        <p role="alert">{loadErrorMessage}</p>
        <button type="button" onClick={() => void load()}>
          Try again
        </button>
      </div>
    )
  }

  if (!event || !index || !bitset) {
    return null // unreachable: status is only 'ready' once all three are set
  }

  return (
    <div>
      <h1>{event.title}</h1>
      <p>{event.artist}</p>

      {/* Best available is the first interactive element after the page
          heading, not buried after the map (docs/plan.md). */}
      <div>
        <label htmlFor="best-available-qty">Number of seats</label>{' '}
        <select
          id="best-available-qty"
          value={bestAvailableQty}
          disabled={!!hold}
          onChange={(e) => setBestAvailableQty(Number(e.target.value))}
        >
          {Array.from({ length: MAX_SEATS }, (_, i) => i + 1).map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>{' '}
        <button type="button" onClick={() => handleBestAvailable(bestAvailableQty)} disabled={!!hold}>
          Find best available
        </button>
      </div>

      <p>You can select up to {MAX_SEATS} seats.</p>

      <div aria-live="polite">{liveMessage}</div>
      {actionError && <p role="alert">{actionError}</p>}

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
        <div>
          <h2>Your selection</h2>
          <ul>
            {selection.map((ordinal) => (
              <li key={ordinal}>
                {shortSeatLabel(index, ordinal)} — ${(priceForTierIdx[index.tierIdx[ordinal]] / 100).toFixed(2)}
              </li>
            ))}
          </ul>
          <p>
            Subtotal ({selection.length} seat{selection.length === 1 ? '' : 's'}): $
            {(selection.reduce((sum, o) => sum + (priceForTierIdx[index.tierIdx[o]] ?? 0), 0) / 100).toFixed(2)}
          </p>
          <button type="button" onClick={() => void handleHold()}>
            Reserve {selection.length} seat{selection.length === 1 ? '' : 's'}
          </button>
        </div>
      )}

      {hold && (
        <div>
          <HoldTimer expiresAt={hold.expiresAt} serverTime={hold.serverTime} onExpire={() => void handleHoldExpire()} />
          <p>
            Subtotal ({hold.seats.length} seat{hold.seats.length === 1 ? '' : 's'}): ${(hold.totalCents / 100).toFixed(2)}
          </p>
          <button type="button" onClick={() => void handleRelease()}>
            Release seats
          </button>
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

/** A short, non-price seat label for lists and status messages — the full
 * priced accessible-name string (seatLabel.ts) is for the a11y tree only. */
function shortSeatLabel(index: SeatIndex, ordinal: number): string {
  const section = sectionOfOrdinal(index, ordinal)
  const row = rowOfOrdinal(index, ordinal)
  return `${section.name}, ${row.label}, seat ${index.seatNumber[ordinal]}`
}
