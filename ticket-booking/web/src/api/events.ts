import { apiFetch, BASE_URL } from './client'
import type { LayoutMeta } from '../seatmap/model/seatIndex'

export interface EventSummary {
  eventId: number
  title: string
  artist: string
  venueId: number
  startsAt: string
  onsaleAt: string
  minPriceCents: number
  availableCount: number
}

export interface EventDetail {
  eventId: number
  title: string
  artist: string
  venueId: number
  startsAt: string
  onsaleAt: string
  layoutVersion: number
  layoutUrl: string
  pricingUrl: string
  wsUrl: string
  saleState: 'pre' | 'queue' | 'onsale' | 'closed'
}

export interface PriceTier {
  tierId: string
  name: string
  priceCents: number
}

export interface PricingResponse {
  priceVersion: number
  tiers: PriceTier[]
  closedSections: number[]
}

export function listEvents(q?: string): Promise<{ events: EventSummary[]; nextCursor: number }> {
  const qs = q ? `?q=${encodeURIComponent(q)}` : ''
  return apiFetch(`/api/v1/events${qs}`)
}

export function getEvent(eventId: number): Promise<EventDetail> {
  return apiFetch(`/api/v1/events/${eventId}`)
}

export function getPricing(eventId: number): Promise<PricingResponse> {
  return apiFetch(`/api/v1/events/${eventId}/pricing`)
}

/** Fetches the packed 2-bit availability bitset directly — the DB-scan
 * path until Phase 7's Redis-backed projector + WS deltas land. */
export async function getAvailability(eventId: number): Promise<Uint8Array> {
  const res = await fetch(`${BASE_URL}/api/v1/events/${eventId}/availability`)
  if (!res.ok) throw new Error(`availability fetch failed: ${res.status}`)
  return new Uint8Array(await res.arrayBuffer())
}

/** Fetches and parses layout.json — the layoutUrl from getEvent() points
 * directly at the S3/CloudFront object, bypassing the API server (real
 * prod shape: the API never proxies static assets). */
export async function fetchLayoutMeta(layoutUrl: string): Promise<LayoutMeta> {
  const res = await fetch(layoutUrl)
  if (!res.ok) throw new Error(`layout.json fetch failed: ${res.status}`)
  return res.json()
}

/** seats.bin lives at the same prefix as layout.json (docs/plan.md
 * "Static layout format" — "same-prefix convention"). */
export async function fetchSeatsBin(layoutUrl: string): Promise<ArrayBuffer> {
  const seatsBinUrl = layoutUrl.replace(/layout\.json$/, 'seats.bin')
  const res = await fetch(seatsBinUrl)
  if (!res.ok) throw new Error(`seats.bin fetch failed: ${res.status}`)
  return res.arrayBuffer()
}
