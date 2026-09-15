import { expect, test } from '@playwright/test'

// Full browser E2E against the real running stack (docker compose + real
// cognito-local + real Postgres/Redis + event_id=1 from earlier phases'
// seed-venue run) — mirrors offline-sync-app's own verification standard:
// zero console errors, real network calls, no mocking.

test.beforeEach(({ page }) => {
  const errors: string[] = []
  page.on('console', (msg) => {
    if (msg.type() === 'error') errors.push(msg.text())
  })
  page.on('pageerror', (err) => errors.push(String(err)))
  page.on('close', () => {
    expect(errors, `console/page errors: ${errors.join('\n')}`).toEqual([])
  })
})

test('browses signed out, then creates an account, confirms, signs in, and reserves a seat', async ({ page }) => {
  const email = `e2e-${Date.now()}@example.com`

  await page.goto('/')

  // Browsing requires no account (Phase 13: only the reserve action does) —
  // the seat map and a11y tree are reachable while signed out.
  await expect(page.getByRole('heading', { name: 'Live in Concert' })).toBeVisible({ timeout: 15000 })
  const firstSectionButton = page.getByRole('button', { name: /Section A, floor tier/ })
  await expect(firstSectionButton).toBeAttached()

  await page.getByRole('button', { name: 'Create account' }).click()
  await page.getByLabel('Email address').fill(email)
  await page.getByLabel('Password').fill('TestPass123!')
  await page.getByRole('button', { name: 'Create account', exact: true }).click()

  await expect(page.getByRole('heading', { name: 'Confirm your email' })).toBeVisible()
  await page.getByRole('button', { name: 'Confirm email' }).click()

  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible()
  await page.getByLabel('Email address').fill(email)
  await page.getByLabel('Password').fill('TestPass123!')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()

  await expect(page.getByText(`Signed in as ${email}`)).toBeVisible()

  // The a11y tree is present and keyboard-navigable — not a visual check,
  // an accessible-name check (the actual claim docs/plan.md makes).
  await firstSectionButton.focus()
  await page.keyboard.press('Enter') // section -> row
  await page.keyboard.press('Enter') // row -> seat

  const focused = page.locator(':focus')
  await expect(focused).toHaveAttribute('aria-label', /Section A, Row 1, seat 1,.*available/)

  // Select the focused seat via the KEYBOARD — this element is
  // intentionally visually hidden (clip-path, not display:none), so a
  // mouse click is the wrong interaction to test here; Enter is what a
  // screen-reader user actually does.
  await page.keyboard.press('Enter')
  await expect(page.locator(':focus')).toHaveAttribute('aria-label', /selected by you/)
  await page.getByRole('button', { name: /Reserve 1 seat/ }).click()

  await expect(page.getByRole('timer')).toBeVisible({ timeout: 10000 })
  await expect(page.getByText(/^Subtotal \(1 seat\): \$/)).toBeVisible()

  await page.getByRole('button', { name: 'Release seats' }).click()
  await expect(page.getByRole('timer')).not.toBeVisible()
})

test('prompts sign-in when reserving while signed out, and preserves the selection through it', async ({ page }) => {
  const email = `e2e-preselect-${Date.now()}@example.com`

  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Live in Concert' })).toBeVisible({ timeout: 15000 })

  const firstSectionButton = page.getByRole('button', { name: /Section A, floor tier/ })
  await firstSectionButton.focus()
  await page.keyboard.press('Enter')
  await page.keyboard.press('Enter')
  await page.keyboard.press('Enter') // select the seat, signed out

  await page.getByRole('button', { name: /Reserve 1 seat/ }).click()

  // Reserving while signed out opens sign-in rather than failing silently.
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible()

  await page.getByRole('button', { name: 'Create an account' }).click()
  await page.getByLabel('Email address').fill(email)
  await page.getByLabel('Password').fill('TestPass123!')
  await page.getByRole('button', { name: 'Create account', exact: true }).click()
  await page.getByRole('button', { name: 'Confirm email' }).click()
  await page.getByLabel('Email address').fill(email)
  await page.getByLabel('Password').fill('TestPass123!')
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()

  // The reserve action the visitor originally requested completes
  // automatically — they never had to reselect their seat.
  await expect(page.getByRole('timer')).toBeVisible({ timeout: 10000 })
  await page.getByRole('button', { name: 'Release seats' }).click()
})
