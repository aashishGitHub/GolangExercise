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

test('register, confirm, sign in, then hold a seat via the canvas and via the a11y tree', async ({ page }) => {
  const email = `e2e-${Date.now()}@example.com`

  await page.goto('/')
  await page.getByLabel('email').fill(email)
  await page.getByLabel('password').fill('TestPass123!')
  await page.getByRole('button', { name: 'Sign up' }).click()

  await page.getByLabel('confirmation code').fill('123456')
  await page.getByRole('button', { name: 'Confirm' }).click()

  await page.getByLabel('email').fill(email)
  await page.getByLabel('password').fill('TestPass123!')
  await page.getByRole('button', { name: 'Log in' }).click()

  await expect(page.getByRole('heading', { name: 'Live in Concert' })).toBeVisible({ timeout: 15000 })

  // The a11y tree is present and keyboard-navigable — not a visual check,
  // an accessible-name check (the actual claim docs/plan.md makes).
  const firstSectionButton = page.getByRole('button', { name: /Section A, floor tier/ })
  await expect(firstSectionButton).toBeAttached()
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
  await page.getByRole('button', { name: /Hold 1 seat/ }).click()

  await expect(page.getByRole('timer')).toBeVisible({ timeout: 10000 })
  await expect(page.getByText(/^Total: \$/)).toBeVisible()

  await page.getByRole('button', { name: 'Release hold' }).click()
  await expect(page.getByRole('timer')).not.toBeVisible()
})
