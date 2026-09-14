import { describe, expect, it } from 'vitest'
import { numberToWords, priceToWords, seatAccessibleLabel } from './seatLabel'

describe('numberToWords', () => {
  it.each([
    [0, 'zero'],
    [4, 'four'],
    [15, 'fifteen'],
    [20, 'twenty'],
    [42, 'forty two'],
    [100, 'one hundred'],
    [120, 'one hundred and twenty'],
    [999, 'nine hundred and ninety nine'],
    [1000, 'one thousand'],
    [1500, 'one thousand five hundred'], // no "and" before a full-hundreds remainder
    [1042, 'one thousand and forty two'], // "and" before a sub-hundred remainder
  ])('numberToWords(%i) = %s', (n, want) => {
    expect(numberToWords(n)).toBe(want)
  })
})

describe('priceToWords', () => {
  it('matches the plan doc\'s canonical example exactly', () => {
    // docs/plan.md: "one hundred and twenty dollars" for $120
    expect(priceToWords(12000)).toBe('one hundred and twenty dollars')
  })

  it('singular "dollar" for exactly $1', () => {
    expect(priceToWords(100)).toBe('one dollar')
  })

  it('includes cents when the price is not a round dollar amount', () => {
    expect(priceToWords(12050)).toBe('one hundred and twenty dollars and fifty cents')
  })
})

describe('seatAccessibleLabel', () => {
  it('matches the plan doc\'s exact format', () => {
    const got = seatAccessibleLabel('Section A', 'row 12', 4, 12000, 'available')
    expect(got).toBe('Section A, row 12, seat 4, one hundred and twenty dollars, available')
  })
})
