import { useEffect, useRef } from 'react'
import { getState, STATE_FREE, STATE_HELD, STATE_SOLD } from '../model/bitset'
import type { SeatIndex } from '../model/seatIndex'
import { seatAccessibleLabel, type SeatA11yState } from './seatLabel'
import { useTreeNavigation } from './useTreeNavigation'
import './visuallyHidden.css'

interface Props {
  index: SeatIndex
  bitset: Uint8Array
  selection: number[]
  priceForTierIdx: number[]
  onSelectSeat: (ordinal: number) => void
  onFocusOrdinal?: (ordinal: number | null) => void
}

function stateFor(bitset: Uint8Array, ordinal: number, selected: boolean): SeatA11yState {
  if (selected) return 'selected by you'
  const s = getState(bitset, ordinal)
  if (s === STATE_FREE) return 'available'
  if (s === STATE_SOLD) return 'sold'
  if (s === STATE_HELD) return 'held by another guest'
  return 'unavailable'
}

/** The hidden a11y tree — docs/plan.md "The hidden a11y tree, concretely".
 * Roving tabindex: exactly one tabindex=0 in the whole tree at any moment,
 * so there are never 30,000 flat tab stops. Only the focused section's
 * rows, and only the focused row's seats, are mounted — the virtualization
 * that keeps this out of the thousands-of-DOM-nodes territory. */
export function SeatMapA11yTree({ index, bitset, selection, priceForTierIdx, onSelectSeat, onFocusOrdinal }: Props) {
  const { focus, setFocus, onKeyDown, rowsOfSection } = useTreeNavigation(index)
  const activeRef = useRef<HTMLButtonElement | HTMLLIElement>(null)

  useEffect(() => {
    activeRef.current?.focus()
    if (focus.level === 'seat') onFocusOrdinal?.(focus.seatOrdinal)
  }, [focus, onFocusOrdinal])

  const rows = rowsOfSection(focus.sectionIdx)

  // Enter/Space's native "activate the focused button" behavior is
  // intentionally suppressed by useTreeNavigation's own preventDefault
  // (it would otherwise double-fire alongside the state machine's own
  // Enter-drills-in handling) — so seat SELECTION at the leaf level has to
  // be wired back in explicitly here, after the section/row navigation.
  function handleKeyDown(e: React.KeyboardEvent) {
    onKeyDown(e)
    if (focus.level === 'seat' && (e.key === 'Enter' || e.key === ' ')) {
      onSelectSeat(focus.seatOrdinal)
    }
  }

  return (
    <div className="visually-hidden" onKeyDown={handleKeyDown}>
      <ul aria-label="Sections">
        {index.meta.sections.map((section) => {
          const isFocused = focus.level === 'section' && focus.sectionIdx === section.idx
          return (
            <li key={section.idx}>
              <button
                ref={isFocused ? (activeRef as React.RefObject<HTMLButtonElement>) : undefined}
                tabIndex={isFocused ? 0 : -1}
                aria-label={`${section.name}, ${section.tier} tier`}
                onClick={() => setFocus({ level: 'section', sectionIdx: section.idx, rowIdxInSection: 0, seatOrdinal: section.firstSeat })}
              >
                {section.name}
              </button>
            </li>
          )
        })}
      </ul>

      {focus.level !== 'section' && (
        <div role="grid" aria-label={index.meta.sections[focus.sectionIdx]?.name} aria-rowcount={rows.length}>
          {rows.map((row, rowIdx) => {
            const isFocusedRow = focus.level === 'row' && rowIdx === focus.rowIdxInSection
            const isCurrentRow = rowIdx === focus.rowIdxInSection

            if (focus.level === 'row' || !isCurrentRow) {
              return (
                <div role="row" key={row.idx} aria-rowindex={rowIdx + 1}>
                  <button
                    ref={isFocusedRow ? (activeRef as React.RefObject<HTMLButtonElement>) : undefined}
                    tabIndex={isFocusedRow ? 0 : -1}
                    aria-label={row.label}
                    onClick={() => setFocus({ level: 'row', sectionIdx: focus.sectionIdx, rowIdxInSection: rowIdx, seatOrdinal: row.firstSeat })}
                  >
                    {row.label}
                  </button>
                </div>
              )
            }

            // Only the focused row's gridcells are mounted.
            return (
              <div role="row" key={row.idx} aria-rowindex={rowIdx + 1}>
                {Array.from({ length: row.seatCount }, (_, i) => {
                  const ordinal = row.firstSeat + i
                  const selected = selection.includes(ordinal)
                  const priceCents = priceForTierIdx[index.tierIdx[ordinal]] ?? 0
                  const label = seatAccessibleLabel(
                    index.meta.sections[focus.sectionIdx]?.name ?? '',
                    row.label,
                    index.seatNumber[ordinal],
                    priceCents,
                    stateFor(bitset, ordinal, selected),
                  )
                  const isFocusedSeat = focus.level === 'seat' && ordinal === focus.seatOrdinal
                  return (
                    <div role="gridcell" key={ordinal} aria-colindex={i + 1}>
                      <button
                        ref={isFocusedSeat ? (activeRef as React.RefObject<HTMLButtonElement>) : undefined}
                        tabIndex={isFocusedSeat ? 0 : -1}
                        aria-label={label}
                        aria-disabled={getState(bitset, ordinal) !== STATE_FREE && !selected}
                        onClick={() => onSelectSeat(ordinal)}
                        onFocus={() => setFocus({ ...focus, level: 'seat', rowIdxInSection: rowIdx, seatOrdinal: ordinal })}
                      >
                        {label}
                      </button>
                    </div>
                  )
                })}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
