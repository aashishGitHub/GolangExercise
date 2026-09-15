import { ApiError } from './client'

/** Maps a backend error `code` (internal/httpapi's {code,message} envelope)
 * to plain-language copy. SEAT_TAKEN is the dominant non-2xx during an
 * on-sale (docs/plan.md) and must read as a normal, recoverable event, not
 * a crash — same treatment for every code here. */
const MESSAGES: Record<string, string> = {
  SEAT_TAKEN: 'Those seats were just taken by someone else.',
  HOLD_EXPIRED: 'Your reservation timed out. Please choose your seats again.',
  no_contiguous_seats: "We couldn't find that many seats together. Try fewer seats, or pick them individually.",
  too_many_seats: 'You can reserve up to 8 seats at a time.',
  duplicate_seat: 'The same seat was selected twice — please try again.',
  gone: 'That reservation no longer exists.',
  forbidden: 'That reservation belongs to a different session.',
  not_admitted: "You're not admitted to the sale yet.",
  already_redeemed: 'This ticket has already been used.',
  bad_signature: "This ticket couldn't be verified.",
  unauthorized: 'Your session expired. Please sign in again.',
  not_found: "We couldn't find that.",
  internal: 'Something went wrong on our end. Please try again.',
}

const FALLBACK = 'Something went wrong. Please try again.'
const NETWORK_FALLBACK = "Can't reach the server. Check that it's running, then try again."

/** Turns any error thrown by api/client.ts into copy safe to show a user.
 * Prefer this over `String(e)`, which leaks raw exception text. */
export function describeError(err: unknown): string {
  if (err instanceof ApiError) {
    return MESSAGES[err.code] ?? err.message ?? FALLBACK
  }
  if (err instanceof TypeError) {
    // fetch() throws a bare TypeError ("Failed to fetch") on network failure.
    return NETWORK_FALLBACK
  }
  return FALLBACK
}
