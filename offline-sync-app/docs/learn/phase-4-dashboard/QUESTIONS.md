# Phase 4 — Dashboard: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [../README.md](../README.md)

## L1 Beginner

**Q1. What does the Phase 4 dashboard show, and why does a field-work app need it?**
It shows how many photos at one location are Good, Moderate or Bad, as a stacked bar plus a text legend. A field worker needs the overall condition of a site at a glance, even with no signal. Source: [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx).

**Q2. What is a "live query" and why use one here?**
A query that re-runs itself when the data it read changes. `useLiveQuery` (from `dexie-react-hooks`) wraps the Dexie lookup, so the bar updates after a local capture or after a pull, with no refresh code. Source: `counts` in [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx).

**Q3. Why does the dashboard read from the device and not from the server?**
So it works offline. The offline-first rule is: capture and read use local Dexie; only sync and pull need the network. MILESTONES proves it: the 1/1/1 breakdown showed before any sync. Source: [schema.ts](../../../web/src/db/schema.ts).

Try it:
```
make compose-up
make migrate
make dev-server
make dev-web
```

**Q4. Why a stacked bar and not a pie?**
The condition scale is ordered (Good, Moderate, Bad), and the project's chart guidance routes part-to-whole to a stacked bar. MILESTONES records this as a deliberate deviation from the plan doc's word "pie". Colours are paired with a text legend, so meaning never depends on colour alone. Source: [MILESTONES.md](../../../MILESTONES.md) Phase 4.

## L2 Junior

**Q5. Trace how the counts get from Dexie to pixels.**
`useLiveQuery` runs an async function: list sites by `locationId`, list photos where `siteAssessmentId` is in those site ids, then `filter` by `condition` three times. `total` is the sum. Each non-zero level becomes a `div` with `width: (count/total)*100%` and its colour from `STATUS_COLORS`. Source: [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx).

**Q6. Trace what happens when the user clicks "Pull latest from server".**
`handlePull` sets `pulling`, awaits `pullLocation(locationId)`. That gets the ID token via `fetchAuthSession`, does `GET /api/locations/{id}`, `put`s the location, then for each site does `GET /api/sites/{id}`, `put`s the site and `bulkPut`s its photos. The live query sees the writes and redraws. Errors land in `pullError`. Source: [pull.ts](../../../web/src/sync/pull.ts).

**Q7. What does the server do for those two GETs?**
Both routes are inside the `verifier.Middleware` group in `NewRouter`. Each parses the UUID (400 on bad id), loads the row (404 on `pgx.ErrNoRows`), loads children with a second query, and JSON-encodes a struct that embeds the row plus a child array. Source: [locations.go](../../../internal/httpapi/locations.go), [sites.go](../../../internal/httpapi/sites.go), [router.go](../../../internal/httpapi/router.go).

> Note: MILESTONES calls this "pull-on-login". In code, `pullLocation` is only called from the button handler. Nothing triggers it on login.

**Q8. Why `location.siteAssessments ?? []` in `pull.ts`?**
The TypeScript type says the field can be `null`. A Go slice that is nil can encode as JSON `null`, so the client guards it. I did not check which sqlc call returns nil versus an empty slice; verify before relying on either. Source: `ServerLocation` in [pull.ts](../../../web/src/sync/pull.ts).

## L3 Mid

**Q9. Why does pull use plain `put` and not re-run last-writer-wins?**
The server already resolved LWW in Phase 2, so the server's row is the authority. Pull mirrors it. The code comment says this. The cost: pull also overwrites a local row that has a newer, not-yet-synced edit (see Q12). Source: comment above `pullLocation` in [pull.ts](../../../web/src/sync/pull.ts).

**Q10. Why count on the client instead of reading a stored aggregate?**
Phase 4 is "read path only, no aggregation table yet". Counting locally is the only way to show data before any sync. Phase 7 later adds a server `location_stats` table via `dashboardaggregator`, but the Phase 4 dashboard does not read it (I found no `location_stats` reference in `web/src`). Source: [MILESTONES.md](../../../MILESTONES.md), [phase 7 FILES.md](../phase-7-event-fanout/FILES.md).

**Q11. How is this phase tested, and what does that prove?**
No automated tests: no web test script, no tests for the two GET handlers. MILESTONES records a manual browser run: capture one photo per condition offline, see 1/1/1, then login, sync, pull, still 1/1/1. That shows pull did not double-count, because `put` and `bulkPut` upsert by `id`. It does not guard against regressions.

Try it:
```
make test
```
This runs `go test ./...` and exercises none of the Phase 4 code. That absence is the point.

## L4 Senior

**Q12. What failure modes does `pullLocation` have?**
- Overwrite: `put` replaces a local row even if it has a newer unsynced edit. `drainRecords` in `syncEngine.ts` reads the current Dexie row when it builds the sync request, so I read that as the local edit being lost. I have not run this; verify with a test.
- No deletes: rows removed on the server stay in Dexie forever.
- Partial pull: sites are fetched in a loop with no transaction. A failure on site 3 leaves sites 1 and 2 updated.
- No retry, no backoff.
Source: [pull.ts](../../../web/src/sync/pull.ts), [syncEngine.ts](../../../web/src/sync/syncEngine.ts).

**Q13. What breaks at 100x scale?**
One request per site is N+1; a location with hundreds of sites means hundreds of sequential round trips, on bad networks. The live query loads every photo row of the location into memory to count it. A server-side count (the Phase 7 `location_stats` table) or a bulk endpoint fixes the first; Dexie `count()` per condition could fix the second. I have not measured either.

**Q14. Who is allowed to read a location?**
Any signed-in user. The handlers check only that a valid JWT exists, not that `created_by` matches `sub`. That is a gap, not a design. Source: [locations.go](../../../internal/httpapi/locations.go).

**Q15. What does this phase NOT solve?**
Photo image download (only metadata and `s3Key` are pulled), deletes, pull on login, restoring the dashboard after reload (`locationId` lives in `useState` in [App.tsx](../../../web/src/App.tsx)), accessibility beyond `aria-label` plus legend (the code's own comment accepts the ~1.8:1 contrast of the warning colour), and any automated test.
