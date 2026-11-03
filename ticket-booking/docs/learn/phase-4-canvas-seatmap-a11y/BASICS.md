# Phase 4 — Canvas Seat Map + Accessibility Tree: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

Phase 4 draws 30,000 seats on a canvas and adds a hidden, keyboard-friendly copy of the same seats for screen readers. It was **scoped down**. Built: the seat index, quadtree, Dexie cache, Canvas2D renderer on the main thread, hidden grid with keyboard navigation, one real browser E2E, 37 unit tests. Not built: Web Worker, OffscreenCanvas, WebGL, viewport culling, axe scan, live-region debounce test, focus-ring pixel test, performance tests, a DOM-node-count test. See [MILESTONES.md](../../../MILESTONES.md) Phase 4.

1. **Canvas** — A canvas is one rectangle in a web page that your code paints pixel by pixel. The browser sees one picture, not 30,000 separate seats. It is like painting on a wall instead of gluing 30,000 stickers.
   In this project: [web/src/seatmap/view/SeatMapCanvas.tsx](../../../web/src/seatmap/view/SeatMapCanvas.tsx)

2. **Canvas2D and the main thread** — Canvas2D is the browser's simple drawing API (`fillRect`, `strokeRect`). The main thread is the one thread that also runs your page's buttons and typing. Drawing there is fine for small jobs but can make the page feel stuck if it gets heavy. It is like one cook who also takes the orders.
   In this project: `ctx.fillRect` in [web/src/seatmap/view/SeatMapCanvas.tsx](../../../web/src/seatmap/view/SeatMapCanvas.tsx)

3. **Web Worker and OffscreenCanvas (not built)** — A Web Worker runs code on a second thread. OffscreenCanvas lets that thread paint. The design wanted this, but this phase did not build it, because there was no measured problem yet. It is like hiring a second cook later, once the kitchen is actually busy.
   In this project: the doc comment in [web/src/seatmap/view/SeatMapCanvas.tsx](../../../web/src/seatmap/view/SeatMapCanvas.tsx)

4. **Viewport culling (not built)** — Culling means drawing only what is on screen. Here every seat is redrawn on every change. It is like repainting the whole wall when one sticker moved.
   In this project: the loop over `index.count` in [web/src/seatmap/view/SeatMapCanvas.tsx](../../../web/src/seatmap/view/SeatMapCanvas.tsx)

5. **Accessibility (a11y)** — Accessibility means people with disabilities can use the app, for example by keyboard only or with a screen reader. "a11y" is short for the 11 letters between "a" and "y". It is like adding ramps and braille next to the stairs.
   In this project: [web/src/seatmap/a11y/SeatMapA11yTree.tsx](../../../web/src/seatmap/a11y/SeatMapA11yTree.tsx)

6. **Screen reader** — A screen reader is software that reads a page aloud and lets people move around with the keyboard. It cannot read a painted canvas. It is like a guide who can only describe things that have names.
   In this project: the canvas is `aria-hidden="true"` and `role="presentation"` in [web/src/seatmap/view/SeatMapCanvas.tsx](../../../web/src/seatmap/view/SeatMapCanvas.tsx)

7. **Accessibility tree** — The browser builds a simplified tree of the page for assistive tools: names, roles, states. A canvas gives it nothing, so this project adds a hidden set of real buttons that mirror the seats. It is like a printed menu for a restaurant whose specials are only painted on the wall.
   In this project: [web/src/seatmap/a11y/SeatMapA11yTree.tsx](../../../web/src/seatmap/a11y/SeatMapA11yTree.tsx)

8. **ARIA roles (grid, row, gridcell)** — ARIA attributes tell assistive tools what an element is. `role="grid"` means a table-like widget, `row` a row, `gridcell` a cell. They are like labels on room doors.
   In this project: `role="grid"` in [web/src/seatmap/a11y/SeatMapA11yTree.tsx](../../../web/src/seatmap/a11y/SeatMapA11yTree.tsx)

9. **Accessible name (`aria-label`)** — The accessible name is what a screen reader says for an element. Here a seat is read like "Section A, Row 1, seat 1, one hundred and twenty dollars, available". The price is spelled in words so different readers say it the same way. It is like a name tag with the whole sentence on it.
   In this project: `seatAccessibleLabel` in [web/src/seatmap/a11y/seatLabel.ts](../../../web/src/seatmap/a11y/seatLabel.ts)

10. **Visually hidden (clip-path), not display:none** — `display:none` removes an element from screen readers too. The `visually-hidden` CSS shrinks the element to 1 pixel and clips it, so it is invisible but still readable and focusable. It is like a whisper: nobody sees it, but the right person hears it.
    In this project: [web/src/seatmap/a11y/visuallyHidden.css](../../../web/src/seatmap/a11y/visuallyHidden.css)

11. **Roving tabindex** — `tabindex="0"` makes an element reachable with the Tab key and `-1` removes it from the Tab order. Roving means exactly one element in the tree has 0, and it moves as you navigate. This avoids 30,000 Tab stops. It is like one "you are here" pin moving on a map.
    In this project: `tabIndex={isFocused ? 0 : -1}` in [web/src/seatmap/a11y/SeatMapA11yTree.tsx](../../../web/src/seatmap/a11y/SeatMapA11yTree.tsx)

12. **Keyboard navigation state machine** — A state machine has a few named states and rules for moving between them. Here the states are section, row, seat. Arrows move, Enter goes deeper, Escape goes back. It is like a lift with floors: you press a button to move between known stops.
    In this project: `useTreeNavigation` in [web/src/seatmap/a11y/useTreeNavigation.ts](../../../web/src/seatmap/a11y/useTreeNavigation.ts)

13. **preventDefault** — Browsers have built-in key behaviour: arrows scroll, Enter clicks a focused button. `preventDefault()` cancels it. Without it, the browser and the custom code both reacted and every Enter was applied twice. This was a real bug found by the browser E2E. It is like two waiters both taking the same order.
    In this project: `e.preventDefault()` in [web/src/seatmap/a11y/useTreeNavigation.ts](../../../web/src/seatmap/a11y/useTreeNavigation.ts)

14. **Virtualized DOM (aria-rowindex)** — Virtualized means only part of a long list is really on the page. Only the focused section's rows and the focused row's seats are mounted. `aria-rowindex` and `aria-colindex` tell assistive tools where each piece sits in the whole. It is like a library showing one shelf while the catalogue still knows all shelves.
    In this project: `aria-rowindex` in [web/src/seatmap/a11y/SeatMapA11yTree.tsx](../../../web/src/seatmap/a11y/SeatMapA11yTree.tsx)

15. **Live region** — A live region (`aria-live="polite"`, or `role="alert"`) is a spot on the page whose text changes are spoken aloud. This page uses it for messages like "Reservation released". It is like a station announcer.
    In this project: `aria-live` and `role="alert"` in [web/src/EventPage.tsx](../../../web/src/EventPage.tsx)

16. **Typed arrays, ArrayBuffer, DataView** — A typed array (like `Int16Array`) is a fast list of fixed-size numbers over a block of raw bytes called an `ArrayBuffer`. `DataView` reads bytes with an explicit byte order. Parsing `seats.bin` this way copies nothing. It is like reading a spreadsheet column straight off the paper.
    In this project: `parseSeatsBin` in [web/src/seatmap/model/seatIndex.ts](../../../web/src/seatmap/model/seatIndex.ts)

17. **SeatIndex vs bitset** — `SeatIndex` holds the unchanging facts (x, y, seat number, tier) built once. The bitset holds the one changing fact (free, held, sold). Keeping them separate means "rebuild the map" and "apply a change" are different code paths. It is like a printed seating plan versus the pencil marks on it.
    In this project: [web/src/seatmap/model/seatIndex.ts](../../../web/src/seatmap/model/seatIndex.ts) and [web/src/seatmap/model/bitset.ts](../../../web/src/seatmap/model/bitset.ts)

18. **Quadtree** — A quadtree splits a flat area into four quarters again and again, so you can find "which seats are near this point" without checking all 30,000. It is like finding a house by country, then city, then street.
    In this project: [web/src/seatmap/model/quadtree.ts](../../../web/src/seatmap/model/quadtree.ts)

19. **Hit testing** — Hit testing means working out which seat a click landed on. The canvas has no seat objects, so the code converts the click to map coordinates and asks the quadtree for the nearest seat. It is like pointing at a photo and asking "who is nearest to my finger".
    In this project: `nearestSeat` in [web/src/seatmap/model/quadtree.ts](../../../web/src/seatmap/model/quadtree.ts)

20. **IndexedDB and Dexie cache** — IndexedDB is a database inside the browser. Dexie is a friendlier wrapper. The layout files are stored there, so a returning visitor skips the download. It is like keeping the floor plan in your pocket after the first visit.
    In this project: [web/src/db/layoutCache.ts](../../../web/src/db/layoutCache.ts)

21. **Polling** — Polling means asking the server again every few seconds ("anything new?"). This page re-fetches availability every 5000 ms, and drops any selected seat that has just been taken. Live pushing came later (Phase 7). It is like checking the oven every five minutes instead of using a timer bell.
    In this project: `setInterval` in [web/src/EventPage.tsx](../../../web/src/EventPage.tsx)

22. **Unit test vs end-to-end (E2E) test** — A unit test checks one function with no browser. An E2E test drives a real browser against the real running system. The project has both: vitest and Playwright. It is like testing a brake pad on a bench versus driving the car.
    In this project: [web/vitest.config.ts](../../../web/vitest.config.ts) and [web/e2e/hold-flow.spec.ts](../../../web/e2e/hold-flow.spec.ts)

23. **CORS (the bug curl missed)** — A browser blocks calls to another origin unless the server allows it. `curl` does not check, so phases 1 to 3 never saw the problem. The browser E2E found it and `go-chi/cors` was added. It is like a rule only the security guard enforces, not the delivery driver.
    In this project: `cors.Handler` in [internal/httpapi/router.go](../../../internal/httpapi/router.go)

24. **Server-authoritative timer** — The countdown shows time left on the hold, but the server decides when a hold is dead. At zero the browser asks the server. It is like a referee's clock versus a fan's watch.
    In this project: `handleHoldExpire` in [web/src/EventPage.tsx](../../../web/src/EventPage.tsx)

25. **Colour alone is not enough** — Some people cannot tell colours apart. The plan asks for patterns as well as colours. The canvas as written fills seats with colour only. It is like a map that uses red and green with no labels.
    In this project: `COLORS` in [web/src/seatmap/view/SeatMapCanvas.tsx](../../../web/src/seatmap/view/SeatMapCanvas.tsx)
