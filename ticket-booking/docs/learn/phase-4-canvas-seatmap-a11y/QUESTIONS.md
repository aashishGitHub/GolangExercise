# Phase 4 — Canvas Seat Map + Accessibility Tree: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [learn index](../README.md)

## L1 Beginner

**Q1. Why can't a screen reader read the seat map canvas, and what does the project do about it?**
A canvas is a grid of painted pixels. The browser exposes no seats, names, or roles from it. The project marks the canvas `aria-hidden="true"` and `role="presentation"` ([web/src/seatmap/view/SeatMapCanvas.tsx](../../../web/src/seatmap/view/SeatMapCanvas.tsx)) and adds a visually hidden set of real buttons that mirror the seats ([web/src/seatmap/a11y/SeatMapA11yTree.tsx](../../../web/src/seatmap/a11y/SeatMapA11yTree.tsx)).

**Q2. What is the accessibility tree and what is a roving tabindex?**
The accessibility tree is the browser's simplified view of the page for assistive tools (names, roles, states). Roving tabindex means exactly one element has `tabindex="0"` and the rest have `-1`, and the 0 moves with focus. The result is one Tab stop, not 30,000. See `tabIndex={isFocused ? 0 : -1}` in [web/src/seatmap/a11y/SeatMapA11yTree.tsx](../../../web/src/seatmap/a11y/SeatMapA11yTree.tsx).

**Q3. Why is the hidden tree hidden with CSS clipping and not `display:none`?**
`display:none` and `aria-hidden` remove the element from the accessibility tree and from focus. The `visually-hidden` class shrinks it to 1 pixel and clips it, so it is invisible but still focusable and readable. See [web/src/seatmap/a11y/visuallyHidden.css](../../../web/src/seatmap/a11y/visuallyHidden.css).

**Q4. What was Phase 4 supposed to deliver, and what did it actually deliver?**
Built: `SeatIndex`, quadtree, Dexie cache, main-thread Canvas2D, hidden grid with keyboard navigation, live regions, best-available button, 37 vitest tests, one browser E2E. Not built: Web Worker, OffscreenCanvas, WebGL, viewport culling, axe scan, live-region debounce test, focus-ring pixel test, performance tests, a 30,000-seat DOM-node-count test. MILESTONES calls this "a scope cut made under time pressure, not a claim that a11y is fully verified". See [MILESTONES.md](../../../MILESTONES.md) Phase 4 and [FILES.md](FILES.md).

## L2 Junior

**Q5. Trace a keyboard-only seat selection through the code.**
Focus a section button, then:
1. Enter: `onKeyDown` in [web/src/seatmap/a11y/useTreeNavigation.ts](../../../web/src/seatmap/a11y/useTreeNavigation.ts) calls `preventDefault`, and `setFocus` moves `level` from `section` to `row`.
2. Enter again: `level` becomes `seat` with `seatOrdinal` = the row's first seat.
3. The effect in `SeatMapA11yTree` calls `.focus()` on the seat button and `onFocusOrdinal`, so the canvas draws a focus ring.
4. Enter again: `handleKeyDown` sees `focus.level === 'seat'` and calls `onSelectSeat(focus.seatOrdinal)`, which is `toggleSeat` in [web/src/EventPage.tsx](../../../web/src/EventPage.tsx).
The E2E checks the focused `aria-label` matches `/Section A, Row 1, seat 1,.*available/` and later `/selected by you/`.

**Q6. Trace a mouse click on the canvas to a seat.**
`handleClick` takes the click position relative to the canvas, `toWorld` converts it to layout coordinates, `nearestSeat(quadtree, index, wx, wy)` returns the ordinal, and `onSelectSeat(ordinal)` runs `toggleSeat`. See [web/src/seatmap/view/SeatMapCanvas.tsx](../../../web/src/seatmap/view/SeatMapCanvas.tsx) and [web/src/seatmap/model/quadtree.ts](../../../web/src/seatmap/model/quadtree.ts).

**Q7. How does the page go from "event 1" to a drawn map, and what does the cache do?**
`load()` in [web/src/EventPage.tsx](../../../web/src/EventPage.tsx) calls `getEvent`, then in parallel `loadSeatIndex`, `getAvailability`, `getPricing`. `loadSeatIndex` ([web/src/db/layoutCache.ts](../../../web/src/db/layoutCache.ts)) looks up Dexie key `venueId:layoutVersion`. A hit builds the index from stored bytes with no network. A miss fetches `layout.json` and `seats.bin`, builds, stores, and evicts old entries.

**Q8. How does a seat get its price label?**
`seats.bin` stores only a `tierIdx` per seat. `layout.json` has `tiers[]` in the same order, the pricing endpoint returns tiers by name, and `priceLookup` in `EventPage` joins them by name. Joining by array position would be wrong because the two orders differ (MILESTONES bug 2, fixed with `LayoutMeta.Tiers` in [internal/catalog/layout.go](../../../internal/catalog/layout.go)).

Try it:
```
npm --prefix web test
```

## L3 Mid

**Q9. Why one `SeatIndex` and one bitset feeding both the canvas and the hidden tree?**
Both views read the same arrays by ordinal, so there is no synchronisation code and the two cannot disagree about which seat is which. The immutable index and the mutable bitset are separate on purpose: rebuilding the map and applying an availability change are different paths ([web/src/seatmap/model/seatIndex.ts](../../../web/src/seatmap/model/seatIndex.ts), [web/src/seatmap/model/bitset.ts](../../../web/src/seatmap/model/bitset.ts)). The cost is that the model must stay a faithful port of the Go wire format.

**Q10. What did the E2E catch that the unit tests could not, and what does it still not prove?**
It caught the missing CORS config (curl does not enforce CORS) and the missing `preventDefault` that made Enter fire twice, per MILESTONES. The `beforeEach` in [web/e2e/hold-flow.spec.ts](../../../web/e2e/hold-flow.spec.ts) fails any test on a console error. It does not prove accessibility. There is no axe scan, no screen-reader run, and no pixel or contrast check. The vitest suite runs in a node environment and renders no component ([web/vitest.config.ts](../../../web/vitest.config.ts)).

Try it:
```
npm --prefix web run e2e
```
(Needs `docker compose up -d`, a seeded venue, and a published event 1.)

**Q11. Why mount only the focused section's rows and the focused row's seats?**
Mounting 30,000 buttons would add that many DOM nodes and Tab candidates. Mounting only what is focused keeps the tree small, and `aria-rowindex` and `aria-colindex` tell assistive tools where the mounted pieces sit. There is no test asserting the node count at 30,000 seats (MILESTONES says so). See [web/src/seatmap/a11y/SeatMapA11yTree.tsx](../../../web/src/seatmap/a11y/SeatMapA11yTree.tsx).

**Q12. What does `seatIndex.test.ts` prove, and where is the claim overstated?**
It proves the parser reads a buffer laid out as `SEAT`, version, layout version, count, then x, y, seatNumber, tierIdx columns, handles negative int16, and rejects bad magic and mismatched layout version or seat count. MILESTONES calls it a parse of "a real Go-encoded seats.bin", but the test builds the buffer with its own TypeScript helper that mirrors the Go layout ([web/src/seatmap/model/seatIndex.test.ts](../../../web/src/seatmap/model/seatIndex.test.ts)). Real Go bytes only reach the parser in the E2E.

## L4 Senior

**Q13. What breaks first at a bigger venue or higher update rate?**
Not measured, so these are hypotheses from the code. The canvas effect redraws every seat with one `fillRect` per ordinal on every change to `bitset`, `selection`, or focus ([web/src/seatmap/view/SeatMapCanvas.tsx](../../../web/src/seatmap/view/SeatMapCanvas.tsx)), all on the main thread, so cost grows linearly with seats and with update rate. `queryRect` exists but nothing uses it, so there is no culling. MILESTONES defers the Worker and culling until Phase 7's delta rate or Phase 10's load harness shows a need. The cost of waiting is that the cut line is a measurement you have not taken yet.

**Q14. What are the failure modes of the layout cache?**
- Eviction ordering uses `cachedAt`, set only when an entry is written. A cache hit does not update it, so eviction is oldest-written, not least-recently-used, even though the docs say LRU ([web/src/db/layoutCache.ts](../../../web/src/db/layoutCache.ts)).
- On a cache hit, `buildSeatIndex` runs on stored bytes. If it throws (corrupt entry), `load()` shows an error and retry hits the same entry. I found no purge-and-refetch path, which [plan.md](../../plan.md) describes.
- The key is `venueId:layoutVersion`. If a layout is rebuilt without bumping the version, clients keep the old one. That is a server-side discipline, not enforced in this code.

**Q15. List what is unverified or suspicious about the accessibility claims.**
I did not run the app or the E2E. These come from reading the code:
- No axe scan (`@axe-core/playwright` is installed but unused) and no screen-reader testing.
- At row level the code renders `role="row"` containing a bare button, not a `gridcell`. I am not certain, but an automated ARIA check may flag that.
- `aria-disabled` is set on taken seats, but `onClick` still calls `onSelectSeat`, and `toggleSeat` does not check state.
- Space may toggle a seat twice at seat level (explicit handler plus native click), since only Enter is cancelled by `preventDefault`. Unverified, no test presses Space.
- Seats are colour-only on the canvas, against the plan's "never colour alone".
- No Home/End keys, and live-region messages are not debounced.
Passing the E2E shows the keyboard path works for one flow, not that the map is accessible.

**Q16. Why defer the Worker and `Renderer` interface, and what would you do before 100x?**
The deferral avoids building an abstraction before a measured need, and Phase 4 had no realtime deltas. Before scaling I would measure paint time per update with a trace, then add culling through the existing quadtree `queryRect`, then consider a worker and a render interface. What this phase does not solve: realtime updates (Phase 7), colour-independent encoding, a real accessibility audit, and an automated check that DOM size stays bounded. The polling loop also means a seat can be shown free for up to 5 seconds after someone else takes it, and the server CAS ([Phase 3](../phase-3-hold-cas/FILES.md)) is what actually prevents a double-sell.
