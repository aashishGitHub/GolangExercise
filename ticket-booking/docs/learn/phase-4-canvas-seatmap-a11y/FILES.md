# Phase 4 — Canvas Seat Map + Accessibility Tree: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

## What was and was not built

Built (verified in code): `SeatIndex` and `Quadtree`, Dexie layout cache with eviction, main-thread Canvas2D renderer, hidden `role="grid"` tree with roving tabindex, a polite live region and an alert region, best-available button, vitest unit tests (37 per MILESTONES), one real-browser Playwright flow.

Not built, as MILESTONES says: Web Worker, OffscreenCanvas, WebGL, a `Renderer` interface, viewport culling or LOD, axe scan, live-region debounce test, focus-ring pixel test, performance tests, a test that DOM node count stays low at 30,000 seats. The grep for `axe` finds nothing in `web/src` or `web/e2e`, although `@axe-core/playwright` is listed in `web/package.json`.

Extra gaps I found that MILESTONES does not list:
- No Home or End key handling in the tree (the plan asks for it).
- No pattern fills: seats are colour-only (`COLORS` in the canvas file), while the plan says "never colour alone".
- No live-region debounce or aggregation in the code, so messages are written to the region immediately.
- A canvas click toggles the nearest seat's selection directly, instead of moving DOM focus to the hidden button as the plan describes.

How tests run: `npm --prefix web test` runs vitest (`environment: 'node'`, only `src/**/*.test.ts`). No component is rendered in any unit test. `npm --prefix web run e2e` runs Playwright against a running stack. CI runs vitest but not Playwright ([ticketing-ci.yml](../../../../.github/workflows/ticketing-ci.yml)).

## [web/src/EventPage.tsx](../../../web/src/EventPage.tsx)

- **What it does**: the page that connects everything: loads the event, layout, availability and pricing, owns seat selection, renders the canvas and the hidden tree, and runs hold, release, and best-available actions.
- **Control flow**:
  1. `App` renders `EventPage` with `eventId={1}`.
  2. `load()` calls `getEvent(eventId)`, then in parallel `loadSeatIndex(venueId, layoutVersion, layoutUrl)` ([web/src/db/layoutCache.ts](../../../web/src/db/layoutCache.ts)), `getAvailability(eventId)`, `getPricing(eventId)` ([web/src/api/events.ts](../../../web/src/api/events.ts)).
  3. `loadSeatIndex` checks Dexie by key `venueId:layoutVersion`. On a miss it fetches `layout.json` and `seats.bin`, calls `buildSeatIndex`, stores both, and runs `evictOldVenues`.
  4. State becomes `ready`. `priceLookup` joins `index.meta.tiers` to the pricing tiers by name.
  5. A `setInterval` every 5000 ms re-fetches availability, sets the bitset, and removes now-taken seats from `selection` with an `aria-live` message.
  6. `SeatMapCanvas` and `SeatMapA11yTree` both get the same `index`, `bitset`, `selection`, and both call `toggleSeat` (max 8 seats).
  7. Reserve: `reserveSeats` gets `idToken()`, `waitForAdmission`, `createHold`. Best available calls `bestAvailable`.
- **Data flow**: server (event JSON, layout files, packed availability bitset, pricing JSON) → `SeatIndex` + `Uint8Array` bitset + price array → two views (canvas pixels, hidden DOM) → user selection → `POST .../holds` → `Hold` state with a `HoldTimer`.
- **How it is tested**: no unit test. Exercised by [web/e2e/hold-flow.spec.ts](../../../web/e2e/hold-flow.spec.ts).
- **Read next**: [web/src/seatmap/model/seatIndex.ts](../../../web/src/seatmap/model/seatIndex.ts).

> Note: this file now also contains waiting-room admission (Phase 8) and sign-in gating (Phase 13). Only the seat-map wiring is Phase 4.

## [web/src/seatmap/model/seatIndex.ts](../../../web/src/seatmap/model/seatIndex.ts)

- **What it does**: builds the immutable `SeatIndex` from `layout.json` metadata and the `seats.bin` buffer. It is a TypeScript port of the Go wire format. Companion [web/src/seatmap/model/bitset.ts](../../../web/src/seatmap/model/bitset.ts) (`getState`, `setState`, `byteLen`) is the mutable availability part.
- **Control flow**:
  1. `buildSeatIndex(meta, buf)` calls `parseSeatsBin(buf)`.
  2. `parseSeatsBin` reads the magic `SEAT` with a `DataView`, then `layoutVersion` (offset 6, little-endian) and `seatCount` (offset 8), then creates `Int16Array`, `Int16Array`, `Uint16Array`, `Uint8Array` views over the same buffer starting at offset 12.
  3. It throws if the seat count or layout version differs from `layout.json`.
  4. `rowOfOrdinal` and `sectionOfOrdinal` scan the row or section lists for the range containing an ordinal.
- **Data flow**: `ArrayBuffer` + `LayoutMeta` → `SeatIndex { x, y, seatNumber, tierIdx, meta }`. No copy of the seat data is made.
- **How it is tested**: unit (vitest), [web/src/seatmap/model/seatIndex.test.ts](../../../web/src/seatmap/model/seatIndex.test.ts): parses a buffer including a negative int16, rejects bad magic, rejects layoutVersion mismatch, rejects seatCount mismatch, and `rowOfOrdinal`/`sectionOfOrdinal` ranges. Bitset: [web/src/seatmap/model/bitset.test.ts](../../../web/src/seatmap/model/bitset.test.ts).
- **Read next**: [web/src/seatmap/model/quadtree.ts](../../../web/src/seatmap/model/quadtree.ts).

> Note: MILESTONES says the test "parses a real Go-encoded seats.bin", and the test comment says it proves the TS parser reads what the Go encoder writes. The test builds its buffer with its own TypeScript `encodeSeatsBin` helper, which mirrors the Go layout. It does not load bytes produced by Go. The real Go bytes are only read by the Playwright E2E.

## [web/src/seatmap/model/quadtree.ts](../../../web/src/seatmap/model/quadtree.ts)

- **What it does**: a spatial index over seat ordinals for rectangle queries and nearest-seat lookup.
- **Control flow**:
  1. `buildQuadtree(index)` starts from `index.meta.bbox` and all ordinals.
  2. `buildNode` stops splitting when a node has at most `MAX_LEAF_SEATS` (32) seats, depth reaches `MAX_DEPTH` (12), or the box is degenerate. Otherwise it splits into four quadrants by midpoint.
  3. `nearestSeat(tree, index, x, y)` visits nodes, skipping any whose closest possible point is farther than the best distance so far.
  4. `queryRect` collects ordinals inside a rectangle.
- **Data flow**: `SeatIndex` x/y arrays → tree of nodes holding `Int32Array` ordinal slices → an ordinal or a list of ordinals.
- **How it is tested**: unit, [web/src/seatmap/model/quadtree.test.ts](../../../web/src/seatmap/model/quadtree.test.ts): three `queryRect` cases (known grid, outside, whole bbox) and three `nearestSeat` cases (exact, off-grid, empty).
- **Read next**: [web/src/seatmap/view/SeatMapCanvas.tsx](../../../web/src/seatmap/view/SeatMapCanvas.tsx).

> Note: `queryRect` is tested but nothing in `web/src` calls it, because viewport culling was not built. The header comment mentions `seatmap/worker/renderer.worker.ts`. That file does not exist. The tree is a recursive node tree, not the flat typed-array buffer the plan describes, and the file says so.

## [web/src/seatmap/view/SeatMapCanvas.tsx](../../../web/src/seatmap/view/SeatMapCanvas.tsx)

- **What it does**: the Canvas2D renderer. Fixed 800 by 600 canvas, `aria-hidden="true"` and `role="presentation"`.
- **Control flow**:
  1. `useMemo` builds the quadtree from `index`, and a `Set` from `selection`.
  2. It computes `scale` to fit the bounding box with 20 pixels of padding.
  3. A `useEffect` redraws on change of `index`, `bitset`, `selectionSet`, `focusOrdinal`, `scale`: `clearRect`, then for every ordinal `fillRect` with a colour chosen from selected or `getState` (free, held, sold, unavailable).
  4. If `focusOrdinal` is set, it draws a white and a black ring around that seat.
  5. `handleClick` converts the click to world coordinates (`toWorld`), calls `nearestSeat`, then `onSelectSeat(ordinal)`.
- **Data flow**: `SeatIndex` + bitset + selection → pixels. Click → ordinal → `toggleSeat` in `EventPage`.
- **How it is tested**: not unit-tested (no DOM in vitest). Drawn in the E2E run, but no assertion checks pixels or colours.
- **Read next**: [web/src/seatmap/a11y/SeatMapA11yTree.tsx](../../../web/src/seatmap/a11y/SeatMapA11yTree.tsx).

> Note: `handleClick` always selects the nearest seat with no distance limit, and `toggleSeat` in `EventPage` does not check the seat's state. A click near a sold seat can add it to the selection. The 5-second poll removes non-free seats, and the server's CAS is the real guard. I did not run this in a browser.

## [web/src/seatmap/a11y/SeatMapA11yTree.tsx](../../../web/src/seatmap/a11y/SeatMapA11yTree.tsx)

- **What it does**: the hidden accessibility tree. A `div.visually-hidden` containing a `ul` of section buttons and, once you drill in, a `role="grid"` of rows and seat buttons. Styles: [web/src/seatmap/a11y/visuallyHidden.css](../../../web/src/seatmap/a11y/visuallyHidden.css) (1-pixel clip, not `display:none`).
- **Control flow**:
  1. `useTreeNavigation(index)` gives `focus`, `setFocus`, `onKeyDown`, `rowsOfSection`.
  2. An effect calls `.focus()` on the element held by `activeRef`, and reports the focused seat through `onFocusOrdinal` (which drives the canvas focus ring).
  3. At section level, all section buttons render. Below that, a grid renders only the focused section's rows. Only the focused row's seats are mounted as `gridcell` buttons.
  4. Each seat button gets `aria-label` from `seatAccessibleLabel(...)` ([web/src/seatmap/a11y/seatLabel.ts](../../../web/src/seatmap/a11y/seatLabel.ts)) and `aria-disabled` when not free and not selected.
  5. `handleKeyDown` first calls the state machine, then, at seat level on Enter or Space, calls `onSelectSeat(focus.seatOrdinal)`. This re-wires seat selection after `preventDefault` stopped the native button click.
- **Data flow**: `SeatIndex` + bitset + selection + `priceForTierIdx` → labels and roles in the DOM. Same arrays as the canvas, a second view of one source.
- **How it is tested**: no component test, no axe scan. The label text and focus behaviour are checked only by the E2E (regex on `aria-label`, `:focus` after Enter).
- **Read next**: [web/src/seatmap/a11y/useTreeNavigation.ts](../../../web/src/seatmap/a11y/useTreeNavigation.ts).

> Note: I could not confirm by reading that Space behaves. `useTreeNavigation` calls `preventDefault` for arrows, Enter, and Escape, not for Space. At seat level, Space runs the explicit `onSelectSeat` and may also trigger the button's native click, which would toggle twice. I have not run it and no test presses Space. You may want to verify this in a browser.

## [web/src/seatmap/a11y/useTreeNavigation.ts](../../../web/src/seatmap/a11y/useTreeNavigation.ts)

- **What it does**: the keyboard state machine. Focus level is `section`, `row`, or `seat`.
- **Control flow**:
  1. `onKeyDown` calls `preventDefault` for ArrowUp/Down/Left/Right, Enter, Escape.
  2. `setFocus` applies a transition: at `section`, up/down change section and Enter goes to `row`; at `row`, up/down change row, Enter goes to `seat` (row's first seat), Escape goes to `section`; at `seat`, left/right move along the row (clamped), up/down jump to the adjacent row at the same position (clamped), Escape goes to `row`.
  3. `rowsOfSection(idx)` filters `index.meta.rows` by `sectionIdx`.
- **Data flow**: key event + current `TreeFocus` → next `TreeFocus { level, sectionIdx, rowIdxInSection, seatOrdinal }`.
- **How it is tested**: no unit test for the hook. Only the E2E presses Enter twice from a section button and checks the focused seat's `aria-label`.
- **Read next**: [web/src/seatmap/a11y/seatLabel.ts](../../../web/src/seatmap/a11y/seatLabel.ts).

## [web/src/seatmap/a11y/seatLabel.ts](../../../web/src/seatmap/a11y/seatLabel.ts)

- **What it does**: builds the spoken name: `Section A, Row 1, seat 4, one hundred and twenty dollars, available`.
- **Control flow**: `seatAccessibleLabel(section, row, seatNumber, priceCents, state)` calls `priceToWords(priceCents)`, which calls `numberToWords(dollars)` (handles 0 to 999,999, larger returns digits) and appends "and N cents" for non-round prices.
- **Data flow**: numbers and strings → one label string.
- **How it is tested**: unit, [web/src/seatmap/a11y/seatLabel.test.ts](../../../web/src/seatmap/a11y/seatLabel.test.ts): `numberToWords`, `priceToWords` (the plan's example, singular dollar, cents), and `seatAccessibleLabel` exact format. MILESTONES counts 15 tests in this file.
- **Read next**: [web/e2e/hold-flow.spec.ts](../../../web/e2e/hold-flow.spec.ts).

## [web/e2e/hold-flow.spec.ts](../../../web/e2e/hold-flow.spec.ts)

- **What it does**: Playwright E2E against the full running stack (Chromium, base URL `http://localhost:5173`, [web/playwright.config.ts](../../../web/playwright.config.ts)).
- **Control flow**: a `beforeEach` collects `console` errors and page errors and asserts none inside a `page.on('close', ...)` callback. I have not verified that an `expect` thrown there fails the test; inject a `console.error` and run the E2E to check. Test 1 (`browses signed out, then creates an account, confirms, signs in, and reserves a seat`): load the page, create an account, confirm, sign in, focus the Section A button, press Enter twice, expect the focused element's `aria-label` to match `/Section A, Row 1, seat 1,.*available/`, press Enter to select, click "Reserve 1 seat", expect a `timer`, click "Release seats". Test 2 (`prompts sign-in when reserving while signed out, and preserves the selection through it`).
- **Data flow**: real browser → Vite → Go server → cognito-local, Postgres, Redis, MinIO. It needs `docker compose up -d`, a seeded venue, and a published event 1.
- **How it is tested**: this is the E2E. It would have caught the CORS bug and the `preventDefault` bug, per MILESTONES.
- **Read next**: back to the [learn index](../README.md) for the next phase.

> Note: MILESTONES records "1 passed". The file now holds two tests, the second added in a later phase (sign-in gating). The Phase 4 flow is test 1.

> Note: `HoldTimer`'s doc comment promises announcements at coarse intervals, but I found no code for that in [web/src/hold/HoldTimer.tsx](../../../web/src/hold/HoldTimer.tsx). It also recomputes `skewMs` from `Date.now()` on every render. I suspect that stops the countdown from advancing correctly. I have not run it, so treat this as unverified.
