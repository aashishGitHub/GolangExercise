import { useCallback, useState } from 'react'
import type { SeatIndex } from '../model/seatIndex'

// The navigation state machine docs/plan.md specifies: section level
// Up/Down moves between sections, Enter drills into rows; row level
// Up/Down moves between rows, Enter drills into seats, Escape returns to
// sections; seat level Left/Right moves seat-to-seat, Up/Down jumps to the
// adjacent row same position, Enter selects, Escape returns to rows. This
// is what makes there be exactly ONE tabindex="0" in the whole tree at any
// moment (roving tabindex) instead of 30,000 flat tab stops.

export type FocusLevel = 'section' | 'row' | 'seat'

export interface TreeFocus {
  level: FocusLevel
  sectionIdx: number
  rowIdxInSection: number // index into that section's own row list
  seatOrdinal: number
}

export function useTreeNavigation(index: SeatIndex) {
  const [focus, setFocus] = useState<TreeFocus>({ level: 'section', sectionIdx: 0, rowIdxInSection: 0, seatOrdinal: index.meta.sections[0]?.firstSeat ?? 0 })

  const rowsOfSection = useCallback(
    (sectionIdx: number) => index.meta.rows.filter((r) => r.sectionIdx === sectionIdx),
    [index],
  )

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      // Every key this handler understands is also a native button
      // activation key (Enter/Space) or a scroll key (arrows) — without
      // this, the browser's own "Enter clicks the focused button" and
      // "arrows scroll the page" semantics fire ALONGSIDE this state
      // machine and silently double-apply transitions.
      if (['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Enter', 'Escape'].includes(e.key)) {
        e.preventDefault()
      }
      setFocus((f) => {
        const sections = index.meta.sections
        if (f.level === 'section') {
          if (e.key === 'ArrowDown') return { ...f, sectionIdx: Math.min(f.sectionIdx + 1, sections.length - 1) }
          if (e.key === 'ArrowUp') return { ...f, sectionIdx: Math.max(f.sectionIdx - 1, 0) }
          if (e.key === 'Enter') return { ...f, level: 'row', rowIdxInSection: 0 }
          return f
        }
        if (f.level === 'row') {
          const rows = rowsOfSection(f.sectionIdx)
          if (e.key === 'ArrowDown') return { ...f, rowIdxInSection: Math.min(f.rowIdxInSection + 1, rows.length - 1) }
          if (e.key === 'ArrowUp') return { ...f, rowIdxInSection: Math.max(f.rowIdxInSection - 1, 0) }
          if (e.key === 'Enter') return { ...f, level: 'seat', seatOrdinal: rows[f.rowIdxInSection]?.firstSeat ?? f.seatOrdinal }
          if (e.key === 'Escape') return { ...f, level: 'section' }
          return f
        }
        // seat level
        const rows = rowsOfSection(f.sectionIdx)
        const row = rows[f.rowIdxInSection]
        if (!row) return f
        const posInRow = f.seatOrdinal - row.firstSeat
        if (e.key === 'ArrowRight') {
          const next = Math.min(posInRow + 1, row.seatCount - 1)
          return { ...f, seatOrdinal: row.firstSeat + next }
        }
        if (e.key === 'ArrowLeft') {
          const next = Math.max(posInRow - 1, 0)
          return { ...f, seatOrdinal: row.firstSeat + next }
        }
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          const delta = e.key === 'ArrowDown' ? 1 : -1
          const nextRowIdx = f.rowIdxInSection + delta
          const nextRow = rows[nextRowIdx]
          if (!nextRow) return f
          const clampedPos = Math.min(posInRow, nextRow.seatCount - 1)
          return { ...f, rowIdxInSection: nextRowIdx, seatOrdinal: nextRow.firstSeat + clampedPos }
        }
        if (e.key === 'Escape') return { ...f, level: 'row' }
        return f
      })
    },
    [index, rowsOfSection],
  )

  return { focus, setFocus, onKeyDown, rowsOfSection }
}
