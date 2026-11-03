# Phase 3 — Photos / S3: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [../README.md](../README.md)

## L1 Beginner

**Q1. What is a presigned URL, and why not upload photos through the API?**
A presigned URL is an S3 link signed by the server so the holder can PUT one specific key for a limited time without credentials (15 minutes here). The phone then sends the bytes straight to S3, so the Go API (running on Lambda in prod) never carries image bytes. The package comment in [s3.go](../../../internal/storage/s3.go) names Lambda payload limits as the reason. Source: `PresignPut` in [s3.go](../../../internal/storage/s3.go), `presignExpiry` in [photos.go](../../../internal/httpapi/photos.go).

**Q2. What is MinIO and why is it in `docker-compose.yml`?**
A free local server that speaks the S3 API. It replaces S3 on a laptop. MILESTONES says LocalStack's image refused to start without an auth token, so S3 was swapped to MinIO; the Go client needed no code change because the endpoint is configurable. Source: [docker-compose.yml](../../../docker-compose.yml).

**Q3. What is the confirm step for?**
The API cannot see the PUT, because it goes to S3. Confirm checks with `HeadObject` that the object exists before the photo row is saved, and returns 409 if it does not. Without it, the database could list photos with no file. Source: `confirmHandler` in [photos.go](../../../internal/httpapi/photos.go), `Exists` in [s3.go](../../../internal/storage/s3.go).

## L2 Junior

**Q4. Trace one photo from capture to the database.**
1. [capture.ts](../../../web/src/capture/capture.ts) `capturePhoto`: `watermarkImage` makes a JPEG blob; one Dexie transaction adds `photos` (with `s3Key: ''`), `photoBlobs`, and an outbox row.
2. User clicks Sync now; [syncEngine.ts](../../../web/src/sync/syncEngine.ts) `drainPhotos` runs.
3. `POST /api/sites/{id}/photos/presign` → `presignHandler` → `store.PresignPut` → `{uploadUrl, s3Key}` where the key is `photos/{siteId}/{photoId}`.
4. `PUT uploadUrl` with the blob and the same `Content-Type`.
5. `POST .../{photoId}/confirm` → `confirmHandler` → `store.Exists` → `svc.Sync` → `applyPhoto` → `UpsertPhoto` ([sync.go](../../../internal/sync/sync.go), [queries.sql](../../../sqlc/queries.sql)).
6. On `applied` the client stores `s3Key` and deletes the blob; the outbox row is deleted either way.

Try it:
```
make compose-up
make migrate
make dev-server
make dev-web
```

**Q5. How does `storage.New` choose between MinIO and real S3?**
By the arguments. If `endpoint != ""` it sets `BaseEndpoint` and `UsePathStyle = true`. If both access key and secret are non-empty it uses static credentials. In prod [config.go](../../../internal/config/config.go) leaves these empty (no default), so the SDK uses real S3 and the Lambda role. Source: [s3.go](../../../internal/storage/s3.go).

**Q6. How is the watermark drawn?**
`createImageBitmap(file)`, a canvas of the same size, `drawImage`, then `strokeText` and `fillText` with the label (lat, lng, ISO time) near the bottom-left, then `canvas.toBlob` as JPEG at quality 0.9. Source: [watermark.ts](../../../web/src/capture/watermark.ts).

Try it:
```
export AWS_ACCESS_KEY_ID=localdevuser
export AWS_SECRET_ACCESS_KEY=localdevpassword
aws --endpoint-url http://localhost:9000 s3 ls s3://fieldsync-photos --recursive
```
(after a sync, lists the uploaded object; credentials are the MinIO ones in [.env.example](../../../.env.example))

## L3 Mid

**Q7. Why does confirm call `svc.Sync` instead of its own insert?**
One implementation of last-write-wins, idempotent replay, and the 10-photo cap. A second insert path would drift. The cost: confirm gets the `TxRunner`, so it also writes a `photo.upserted` event row, and `resp.Photos[0]` is the only result it reads. Source: [photos.go](../../../internal/httpapi/photos.go), [sync.go](../../../internal/sync/sync.go).

**Q8. Why presign, PUT, confirm rather than the phone sending metadata first?**
The database row should only exist when the file does. If metadata were saved first, a failed PUT would leave a row pointing at nothing. Confirm-after-PUT makes the row the commit point. Retries are safe: the key is deterministic (`photos/{siteId}/{photoId}`), so a repeat PUT overwrites the same object and a repeat confirm is an idempotent upsert. Source: `drainPhotos` in [syncEngine.ts](../../../web/src/sync/syncEngine.ts).

**Q9. How is this phase tested, and what does that prove?**
Only indirectly. `TestSync_Photo_Applied`, `TestSync_Photo_LimitExceeded`, `TestSync_Photo_IgnoredStale` (unit, gomock) and `TestIntegration_FullSyncFlow` (`-tags=integration`, real Postgres, 10 photos then the 11th rejected) cover the database side. Nothing tests `storage`, `presignHandler`, `confirmHandler`, `watermarkImage`, or `drainPhotos`. The browser E2E and the MinIO `aws s3 ls` check in MILESTONES were manual, and no Playwright files are committed. So the S3 path is proven to have worked once, not guarded against regression.

Try it:
```
go test ./internal/sync/...
go test -tags=integration ./internal/sync/...
```
(the second needs `make compose-up` and `make migrate`)

## L4 Senior

**Q10. What does `confirmHandler` trust that it should check?**
- The `s3Key` comes from the request body. It is not compared with `photos/{siteId}/{photoId}`, so a client can confirm a photo against any key that exists in the bucket, including another user's object.
- The site id in the path is never looked up in the presign step, and a confirm for a missing site fails inside the upsert (foreign key error, a 500), not as a 404.
- `condition` is only checked by the database `CHECK`, which also surfaces as a 500.
- No ownership check ties the site to the caller.
Not solved. Source: [photos.go](../../../internal/httpapi/photos.go).

**Q11. What may break in real AWS that MinIO hides?**
Two things I could not confirm from this repo, so verify them against AWS docs. First: the Lambda role in [modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf) grants `s3:PutObject` and `s3:GetObject` on `bucket/*` only. I believe S3 returns 403 instead of 404 for a missing key when the caller lacks `s3:ListBucket`, in which case `Exists` would return an error (not `types.NotFound`) and confirm would answer 500 instead of 409. Second: I found no CORS configuration for the photos bucket in `infra/terraform` (the only `cors_configuration` is on the API in `modules/api`), and a browser PUT from another origin to S3 normally needs one. MinIO worked in the E2E run; real S3 was not exercised (MILESTONES: `apply` is never run automatically).

**Q12. What failure modes leave junk behind?**
- PUT succeeds but the client dies before confirm: the object sits in S3 with no row. The next sync retry overwrites the same key, but if the client never retries the object stays orphaned. No cleanup job exists.
- Confirm returns `limit_exceeded`, `ignored_stale`, or `conflict`: the outbox row is deleted, the Dexie blob is kept (only `applied` deletes it), and the S3 object stays. That is a leak in both places.
- Presign signs `Content-Type`, but nothing limits size or validates that the bytes are an image. I believe a presigned PUT cannot cap size (a presigned POST policy can); verify in the S3 docs.

**Q13. What breaks at 100x, and what is not claimed?**
- API load stays small per photo (two tiny calls plus one HEAD) because bytes bypass the API. That is the point of the design.
- `drainPhotos` uploads one photo at a time, sequentially; a backlog of large photos on a bad network is slow, and any throw aborts the loop, leaving later entries for the next run.
- Every applied photo inserts a `domain_events` row (via `TxRunner`), so write volume to Postgres grows with photos.
- The geotag is typed into text fields, not read from GPS, and the watermark is drawn in the browser, so it is not tamper-proof evidence.
- No thumbnails or serving path exist in this phase (the Terraform CloudFront for reads is separate). I have no throughput numbers for photos; MILESTONES gives none.
