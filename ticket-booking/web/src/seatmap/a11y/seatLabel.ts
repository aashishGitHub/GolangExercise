// Accessible name builder — docs/plan.md: "Section A, row 12, seat 4, one
// hundred and twenty dollars, available". Price is spelled in words to
// remove cross-screen-reader pronunciation variance for "$120"; digits stay
// in the visible tooltip (not built here — canvas-side hover, later phase).

const ONES = [
  'zero', 'one', 'two', 'three', 'four', 'five', 'six', 'seven', 'eight', 'nine',
  'ten', 'eleven', 'twelve', 'thirteen', 'fourteen', 'fifteen', 'sixteen', 'seventeen', 'eighteen', 'nineteen',
]
const TENS = ['', '', 'twenty', 'thirty', 'forty', 'fifty', 'sixty', 'seventy', 'eighty', 'ninety']

function threeDigitsToWords(n: number): string {
  const parts: string[] = []
  if (n >= 100) {
    parts.push(ONES[Math.floor(n / 100)], 'hundred')
    n %= 100
  }
  if (n >= 20) {
    parts.push(TENS[Math.floor(n / 10)])
    n %= 10
    if (n > 0) parts.push(ONES[n])
  } else if (n > 0) {
    parts.push(ONES[n])
  }
  return parts.join(' ')
}

/** Spells an integer dollar amount in words: 120 -> "one hundred and
 * twenty". Handles 0 through 999,999 — comfortably past any real ticket
 * price; anything larger falls back to plain digits rather than guessing
 * at a word form nobody will actually hit. */
export function numberToWords(n: number): string {
  if (n === 0) return 'zero'
  if (n < 0) return `negative ${numberToWords(-n)}`
  if (n >= 1_000_000) return String(n)

  if (n >= 1000) {
    const thousands = Math.floor(n / 1000)
    const rest = n % 1000
    const thousandsWords = `${threeDigitsToWords(thousands)} thousand`
    return rest === 0 ? thousandsWords : `${thousandsWords} ${rest < 100 ? 'and ' : ''}${threeDigitsToWords(rest)}`
  }
  if (n >= 100) {
    const hundreds = Math.floor(n / 100)
    const rest = n % 100
    return rest === 0 ? `${ONES[hundreds]} hundred` : `${ONES[hundreds]} hundred and ${threeDigitsToWords(rest)}`
  }
  return threeDigitsToWords(n)
}

/** priceCents (e.g. 12050) -> "one hundred and twenty dollars" (cents are
 * dropped from the spoken form for round ticket prices — the overwhelming
 * common case; a non-round price falls back to "and N cents"). */
export function priceToWords(priceCents: number): string {
  const dollars = Math.floor(priceCents / 100)
  const cents = priceCents % 100
  const dollarWord = dollars === 1 ? 'dollar' : 'dollars'
  if (cents === 0) return `${numberToWords(dollars)} ${dollarWord}`
  return `${numberToWords(dollars)} ${dollarWord} and ${numberToWords(cents)} cents`
}

export type SeatA11yState = 'available' | 'held' | 'sold' | 'unavailable' | 'held by another guest' | 'selected by you'

/** Builds the exact accessible name string, per docs/plan.md's format. */
export function seatAccessibleLabel(sectionName: string, rowLabel: string, seatNumber: number, priceCents: number, state: SeatA11yState): string {
  return `${sectionName}, ${rowLabel}, seat ${seatNumber}, ${priceToWords(priceCents)}, ${state}`
}
