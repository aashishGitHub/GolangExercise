import { useEffect, useMemo, useRef } from 'react'
import { getState, STATE_FREE, STATE_HELD, STATE_SOLD } from '../model/bitset'
import { buildQuadtree, nearestSeat } from '../model/quadtree'
import type { SeatIndex } from '../model/seatIndex'

interface Props {
  index: SeatIndex
  bitset: Uint8Array
  selection: number[]
  focusOrdinal: number | null
  onSelectSeat: (ordinal: number) => void
}

const COLORS = {
  free: '#2e7d32',
  held: '#f9a825',
  sold: '#9e9e9e',
  unavailable: '#424242',
  selected: '#1565c0',
}

/** Main-thread Canvas2D renderer — no Web Worker / OffscreenCanvas / WebGL
 * yet (docs/plan.md asks for those; deferred here and noted honestly in
 * MILESTONES.md as a scope simplification for this pass, not implemented
 * as if it were). Redraws on demand (selection/bitset change), not a
 * continuous rAF loop, since there's no animation without realtime deltas
 * (Phase 7). No viewport culling/LOD yet either — acceptable at interactive
 * scale for a static full redraw; add culling when perf numbers demand it. */
export function SeatMapCanvas({ index, bitset, selection, focusOrdinal, onSelectSeat }: Props) {
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const quadtree = useMemo(() => buildQuadtree(index), [index])
  const selectionSet = useMemo(() => new Set(selection), [selection])

  const [minX, minY, maxX, maxY] = index.meta.bbox
  const width = 800
  const height = 600
  const pad = 20
  const scale = Math.min((width - 2 * pad) / Math.max(1, maxX - minX), (height - 2 * pad) / Math.max(1, maxY - minY))

  function toScreen(x: number, y: number): [number, number] {
    return [pad + (x - minX) * scale, pad + (y - minY) * scale]
  }
  function toWorld(sx: number, sy: number): [number, number] {
    return [(sx - pad) / scale + minX, (sy - pad) / scale + minY]
  }

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    ctx.clearRect(0, 0, width, height)
    const seatSize = Math.max(2, scale * 0.7)

    for (let ordinal = 0; ordinal < index.count; ordinal++) {
      const [sx, sy] = toScreen(index.x[ordinal], index.y[ordinal])
      const selected = selectionSet.has(ordinal)
      const state = getState(bitset, ordinal)
      ctx.fillStyle = selected
        ? COLORS.selected
        : state === STATE_FREE
          ? COLORS.free
          : state === STATE_HELD
            ? COLORS.held
            : state === STATE_SOLD
              ? COLORS.sold
              : COLORS.unavailable
      ctx.fillRect(sx - seatSize / 2, sy - seatSize / 2, seatSize, seatSize)
    }

    if (focusOrdinal !== null) {
      const [fx, fy] = toScreen(index.x[focusOrdinal], index.y[focusOrdinal])
      ctx.strokeStyle = '#ffffff'
      ctx.lineWidth = 3
      ctx.strokeRect(fx - seatSize / 2 - 2, fy - seatSize / 2 - 2, seatSize + 4, seatSize + 4)
      ctx.strokeStyle = '#000000'
      ctx.lineWidth = 1
      ctx.strokeRect(fx - seatSize / 2 - 4, fy - seatSize / 2 - 4, seatSize + 8, seatSize + 8)
    }
  }, [index, bitset, selectionSet, focusOrdinal, scale])

  function handleClick(e: React.MouseEvent<HTMLCanvasElement>) {
    const rect = canvasRef.current!.getBoundingClientRect()
    const [wx, wy] = toWorld(e.clientX - rect.left, e.clientY - rect.top)
    const ordinal = nearestSeat(quadtree, index, wx, wy)
    if (ordinal !== null) onSelectSeat(ordinal)
  }

  return <canvas ref={canvasRef} width={width} height={height} onClick={handleClick} aria-hidden="true" role="presentation" />
}
