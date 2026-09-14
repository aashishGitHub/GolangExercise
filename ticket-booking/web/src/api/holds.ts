import { apiFetch } from './client'

export interface HeldSeat {
  seatId: number
  seatOrdinal: number
  priceCents: number
}

export interface Hold {
  holdId: string
  eventId: number
  seats: HeldSeat[]
  expiresAt: string
  serverTime: string
  totalCents: number
  fenceTokens: Record<string, string>
}

export interface ConflictBody {
  code: 'SEAT_TAKEN'
  message: string
  conflicts: number[]
}

export function createHold(eventId: number, seatOrdinals: number[], token: string): Promise<Hold> {
  return apiFetch(`/api/v1/events/${eventId}/holds`, { method: 'POST', token, body: { seatOrdinals } })
}

export function bestAvailable(
  eventId: number,
  quantity: number,
  maxPriceCents: number,
  token: string,
): Promise<Hold> {
  return apiFetch(`/api/v1/events/${eventId}/holds`, {
    method: 'POST',
    token,
    body: { quantity, maxPriceCents, bestAvailable: true },
  })
}

export interface HoldStatus {
  holdId: string
  eventId: number
  status: string
  expiresAt: string
  secondsRemaining: number
  seats: HeldSeat[]
  totalCents: number
}

export function getHold(holdId: string, token: string): Promise<HoldStatus> {
  return apiFetch(`/api/v1/holds/${holdId}`, { token })
}

export function releaseHold(holdId: string, token: string): Promise<void> {
  return apiFetch(`/api/v1/holds/${holdId}`, { method: 'DELETE', token })
}

export function extendHold(holdId: string, token: string, extendSeconds?: number): Promise<{ expiresAt: string; serverTime: string }> {
  return apiFetch(`/api/v1/holds/${holdId}/extend`, { method: 'POST', token, body: extendSeconds ? { extendSeconds } : {} })
}
