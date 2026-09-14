// Spatial index over seat ordinals for O(log n) hit-testing and viewport
// culling (docs/plan.md "Threading" — built once on main from the layout,
// queried synchronously; a structurally-cloned copy goes to the worker for
// its own culling during painting, see seatmap/worker/renderer.worker.ts).
//
// Implementation note, stated rather than glossed over: this is a
// recursive node tree over typed-array ordinal slices, not a single
// contiguous flat buffer (the "flat typed arrays" in the fuller design
// doc). At this data scale (tens of thousands of seats) the allocation and
// cache-locality difference is not the bottleneck; a genuinely flat
// single-buffer layout is a worthwhile future optimization if profiling
// ever shows otherwise, not a correctness requirement.

import type { SeatIndex } from './seatIndex'

const MAX_LEAF_SEATS = 32
const MAX_DEPTH = 12

interface QuadNode {
  minX: number
  minY: number
  maxX: number
  maxY: number
  // Leaf: ordinals lives here, children is null. Internal: ordinals is
  // null, children has exactly 4 entries (NW, NE, SW, SE).
  ordinals: Int32Array | null
  children: [QuadNode, QuadNode, QuadNode, QuadNode] | null
}

export interface Quadtree {
  root: QuadNode
}

export function buildQuadtree(index: SeatIndex): Quadtree {
  const [minX, minY, maxX, maxY] = index.meta.bbox
  const allOrdinals = new Int32Array(index.count)
  for (let i = 0; i < index.count; i++) allOrdinals[i] = i
  return { root: buildNode(index, allOrdinals, minX, minY, maxX, maxY, 0) }
}

function buildNode(
  index: SeatIndex,
  ordinals: Int32Array,
  minX: number,
  minY: number,
  maxX: number,
  maxY: number,
  depth: number,
): QuadNode {
  if (ordinals.length <= MAX_LEAF_SEATS || depth >= MAX_DEPTH || maxX <= minX || maxY <= minY) {
    return { minX, minY, maxX, maxY, ordinals, children: null }
  }

  const midX = (minX + maxX) / 2
  const midY = (minY + maxY) / 2
  const buckets: number[][] = [[], [], [], []] // NW, NE, SW, SE
  for (const ord of ordinals) {
    const x = index.x[ord]
    const y = index.y[ord]
    const quadrant = (x < midX ? 0 : 1) + (y < midY ? 0 : 2)
    buckets[quadrant].push(ord)
  }

  const children: [QuadNode, QuadNode, QuadNode, QuadNode] = [
    buildNode(index, Int32Array.from(buckets[0]), minX, minY, midX, midY, depth + 1),
    buildNode(index, Int32Array.from(buckets[1]), midX, minY, maxX, midY, depth + 1),
    buildNode(index, Int32Array.from(buckets[2]), minX, midY, midX, maxY, depth + 1),
    buildNode(index, Int32Array.from(buckets[3]), midX, midY, maxX, maxY, depth + 1),
  ]
  return { minX, minY, maxX, maxY, ordinals: null, children }
}

/** All ordinals whose (x,y) falls within [minX,maxX] x [minY,maxY]. */
export function queryRect(tree: Quadtree, index: SeatIndex, minX: number, minY: number, maxX: number, maxY: number): number[] {
  const out: number[] = []
  visit(tree.root)
  return out

  function visit(node: QuadNode) {
    if (node.maxX < minX || node.minX > maxX || node.maxY < minY || node.minY > maxY) return
    if (node.ordinals) {
      for (const ord of node.ordinals) {
        const x = index.x[ord]
        const y = index.y[ord]
        if (x >= minX && x <= maxX && y >= minY && y <= maxY) out.push(ord)
      }
      return
    }
    for (const child of node.children!) visit(child)
  }
}

/** The nearest seat to (x,y), by Euclidean distance — used for both
 * canvas-click hit resolution and "nearest equivalent seat" suggestions
 * after a lost race (docs/plan.md, script.md line 148). */
export function nearestSeat(tree: Quadtree, index: SeatIndex, x: number, y: number): number | null {
  let best: number | null = null
  let bestDist = Infinity
  visit(tree.root)
  return best

  function visit(node: QuadNode) {
    // Prune: if the closest possible point in this node's box is already
    // farther than the best found so far, skip the whole subtree.
    const dx = Math.max(node.minX - x, 0, x - node.maxX)
    const dy = Math.max(node.minY - y, 0, y - node.maxY)
    if (dx * dx + dy * dy > bestDist) return

    if (node.ordinals) {
      for (const ord of node.ordinals) {
        const sx = index.x[ord]
        const sy = index.y[ord]
        const d = (sx - x) * (sx - x) + (sy - y) * (sy - y)
        if (d < bestDist) {
          bestDist = d
          best = ord
        }
      }
      return
    }
    for (const child of node.children!) visit(child)
  }
}
