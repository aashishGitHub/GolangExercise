# Phase 4 — Dashboard: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

Phase 4 adds one screen: a bar that shows how many photos at a location are Good, Moderate or Bad. It reads from the phone's own database, so it works with no network.

1. **Dashboard** — A screen that turns many records into one picture you can read fast. Here it shows photo counts per condition for one location. Think of the fuel gauge in a car: one glance, no maths.
   In this project: [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx)

2. **Offline-first read path** — The screen reads data stored on the device, not from the server. So it still works in a tunnel with no signal. Think of a paper map in your bag versus a map app that needs 4G.
   In this project: [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx) (the count query reads only Dexie, `db.siteAssessments` and `db.photos`; only the Pull button reaches the network)

3. **IndexedDB** — A database that lives inside the browser. It can hold lots of structured data and files, and it survives page reloads. Think of a small filing cabinet bolted to the browser.
   In this project: [schema.ts](../../../web/src/db/schema.ts)

4. **Dexie** — A small library that makes IndexedDB easy to use. You write `db.photos.put(...)` instead of the long, raw IndexedDB calls. Think of a remote control for a TV that has 40 hidden buttons.
   In this project: [schema.ts](../../../web/src/db/schema.ts) (class `FieldSyncDB`)

5. **Table and index** — A table is a list of records of one kind. An index is a pre-sorted lookup so a search by one field is fast. Think of the index at the back of a book. This app indexes `locationId` on sites and `siteAssessmentId` on photos.
   In this project: [schema.ts](../../../web/src/db/schema.ts) (the `stores({...})` call)

6. **React hook** — A function whose name starts with `use` that lets a screen plug into React features. React is the library that draws the screen. Think of a power socket on the wall: the screen plugs in and gets live data.
   In this project: [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx) (`useState`, `useLiveQuery`)

7. **Live query** — A database question that re-runs by itself when the answer might have changed. `useLiveQuery` from `dexie-react-hooks` does this for Dexie. You never write "refresh". Think of a scoreboard that updates when a goal is scored.
   In this project: [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx) (`counts`)

8. **Default while loading** — A live query has no answer on the first draw. The code adds `?? { good: 0, moderate: 0, bad: 0 }` so the screen has numbers to show right away. Think of a counter that starts at zero before the first customer.
   In this project: [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx)

9. **Derived data** — A number you work out from other data each time, instead of storing it. The dashboard counts photos by condition on every run. Think of adding up a receipt instead of keeping a separate "total" note that can go wrong.
   In this project: [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx) (`photos.filter(...).length`)

10. **Part-to-whole chart** — A chart that shows how pieces make up a total. For three ordered levels (Good, Moderate, Bad), this project uses one horizontal stacked bar, not a pie. Think of a chocolate bar snapped into three pieces, in order.
    In this project: [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx) (`ORDER`, the flex `div`)

11. **Never colour alone** — Some people cannot tell red from green. So every colour also has a text label and a count. Think of traffic lights that also have the words STOP and GO.
    In this project: [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx) (the `<ul>` legend and `aria-label`)

12. **aria-label** — Hidden text that a screen reader reads out loud for something with no words, like a bar. `role="img"` tells the reader "treat this as one picture". Think of a caption under a photo for a blind reader.
    In this project: [LocationDashboard.tsx](../../../web/src/dashboard/LocationDashboard.tsx)

13. **Pull** — Fetching the server's current data and copying it into the device's database. Pulling lets you see what other workers synced from their phones. Think of checking the shared team notebook and copying new pages into yours.
    In this project: [pull.ts](../../../web/src/sync/pull.ts) (`pullLocation`)

14. **Bearer token (JWT)** — A signed ID card string you send in the `Authorization` header so the server knows who you are. The server checks the signature on every request. Think of a festival wristband.
    In this project: [pull.ts](../../../web/src/sync/pull.ts) (`headers = { Authorization: ... }`)

15. **put and bulkPut (upsert)** — `put` saves a record: it adds it if new, or replaces it if the same `id` exists. `bulkPut` does this for a whole list in one call. Think of "save" in a text editor: new file or overwrite.
    In this project: [pull.ts](../../../web/src/sync/pull.ts)

16. **Last-writer-wins (LWW)** — When two copies of a record differ, the one with the newer `updatedAt` time wins. The server decides this in Phase 2. Pull does not decide again; it trusts the server's answer. Think of two people editing a sign-up sheet: the latest edit stays.
    In this project: [pull.ts](../../../web/src/sync/pull.ts) (comment above `pullLocation`)

17. **Wire shape** — The exact JSON field names that travel between client and server, like `siteAssessmentId`. Both sides must agree or data silently goes missing. Think of both people using the same address format on an envelope.
    In this project: [pull.ts](../../../web/src/sync/pull.ts) (the `Server*` interfaces) and [models.go](../../../internal/db/models.go) (the `json:` tags)

18. **N+1 requests** — One request to get a list, then one more request per item in the list. It is simple, but slow when the list is long. Think of asking a shop for the menu, then asking again for the price of each dish.
    In this project: [pull.ts](../../../web/src/sync/pull.ts) (one `GET /api/locations/{id}`, then one `GET /api/sites/{id}` per site)

19. **null versus empty list** — A server can send `null` where you expect `[]`. Code that loops over `null` crashes. So the code writes `?? []` to treat null as empty. Think of an empty shelf versus no shelf at all.
    In this project: [pull.ts](../../../web/src/sync/pull.ts) (`location.siteAssessments ?? []`)

20. **Read-only, no aggregation table yet** — Phase 4 counts photos on the device. It does not use the server's `location_stats` table. That table arrives in Phase 7. Think of counting coins by hand before buying a coin counter.
    In this project: [MILESTONES.md](../../../MILESTONES.md) (Phase 4 and Phase 7 sections)
