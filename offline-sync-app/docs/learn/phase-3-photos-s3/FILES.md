# Phase 3 — Photos / S3: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

> Note: nothing in this phase has a committed test. There is no `_test.go` for `internal/storage` or the photo handlers, and no JavaScript tests. The MILESTONES "Full browser E2E verified" and the `aws s3 ls` cross-check were manual runs. The only committed tests that touch photos are the `Photo` cases in [internal/sync/sync_test.go](../../../internal/sync/sync_test.go) and the 10-photo cap in [integration_test.go](../../../internal/sync/integration_test.go).

> Note: MILESTONES says "geotag metadata capture". The code has no `navigator.geolocation` call anywhere in `web/src`. The latitude and longitude come from two text inputs in `CapturePanel.tsx` (defaults `1.0` and `2.0`).

> Note: the comment on `EnsureBucket` in [s3.go](../../../internal/storage/s3.go) still says "against LocalStack". The compose file runs MinIO; the comment is stale.

## [internal/storage/s3.go](../../../internal/storage/s3.go)

- **What it does**: A small wrapper over AWS SDK v2 S3: `New`, `PresignPut`, `Exists`, `EnsureBucket`.
- **Control flow**:
  1. [cmd/server/main.go](../../../cmd/server/main.go) calls `storage.New(ctx, cfg.S3Endpoint, cfg.AWSRegion, cfg.S3Bucket, cfg.S3AccessKey, cfg.S3SecretKey)`, then `store.EnsureBucket(ctx)`.
  2. `New` loads default AWS config with the region; adds a static credentials provider only if both keys are set; builds `s3.NewFromConfig`, and if `endpoint != ""` sets `BaseEndpoint` and `UsePathStyle = true`.
  3. `PresignPut` calls `presign.PresignPutObject` with `Bucket`, `Key`, `ContentType` and `s3.WithPresignExpires(expires)`, and returns `req.URL`.
  4. `Exists` calls `HeadObject`. No error returns true; an error that matches `*types.NotFound` returns false; any other error is returned.
  5. `EnsureBucket` calls `HeadBucket`, and on any error tries `CreateBucket`. [cmd/lambda/main.go](../../../cmd/lambda/main.go) deliberately skips it.
- **Data flow**: config strings → S3 client. `PresignPut`: key + content type → signed URL string (nothing is stored server-side). `Exists`: key → bool.
- **How it is tested**: untested. Verified live per MILESTONES (confirming without an upload gave 409).
- **Read next**: [internal/httpapi/photos.go](../../../internal/httpapi/photos.go).

## [internal/httpapi/photos.go](../../../internal/httpapi/photos.go)

- **What it does**: `presignHandler` (`POST /api/sites/{id}/photos/presign`) and `confirmHandler` (`POST /api/sites/{id}/photos/{photoId}/confirm`).
- **Control flow (presign)**:
  1. Router mounts it inside the verified `/api` group ([router.go](../../../internal/httpapi/router.go)).
  2. `uuid.Parse(chi.URLParam(r, "id"))` (400 on failure); decode `presignRequest{photoId, contentType}` (400 if bad or `photoId` is nil).
  3. Empty content type defaults to `image/jpeg`.
  4. `key := fmt.Sprintf("photos/%s/%s", siteID, req.PhotoID)`; `store.PresignPut(ctx, key, contentType, presignExpiry)` where `presignExpiry` is 15 minutes.
  5. Reply `{uploadUrl, s3Key}`. The site id only namespaces the key; no database lookup happens here.
- **Control flow (confirm)**:
  1. Parse `id` and `photoId` (400 on failure); decode `confirmRequest` (400 on bad body).
  2. `store.Exists(ctx, req.S3Key)`: error gives 500 `storage check failed`; false gives 409.
  3. `auth.FromContext` for `claims.Sub`; build a `sync.Request` with one `PhotoInput`; call `svc.Sync(...)`.
  4. Error gives 500 `confirm failed`; otherwise reply `resp.Photos[0]` (one `RecordResult`).
- **Data flow**: presign: path + body → signed URL. Confirm: body (`s3Key`, lat, lng, condition, timestamps) → `PhotoInput` → upsert → outcome JSON. The `s3Key` comes from the client; the handler does not check that it equals `photos/{siteId}/{photoId}`.
- **How it is tested**: untested at the HTTP layer.
- **Read next**: [internal/sync/sync.go](../../../internal/sync/sync.go).

## [internal/sync/sync.go](../../../internal/sync/sync.go)

- **What it does**: For this phase, `applyPhoto` is what confirm reuses. Same code as `/api/sync`.
- **Control flow**:
  1. `confirmHandler` calls `svc.Sync` where `svc` is a `sync.Syncer` (the `TxRunner`; see [Phase 2 FILES](../phase-2-sync-api-db/FILES.md)).
  2. `Service.Sync` loops `req.Photos` and calls `applyPhoto`.
  3. `applyPhoto` calls `q.UpsertPhoto`. No error gives `applied`.
  4. `pgx.ErrNoRows` means blocked. `q.GetPhoto` returning `pgx.ErrNoRows` gives `limit_exceeded` (the id never existed, so the 10-photo cap blocked it). Otherwise `classifyBlocked`.
  5. `TxRunner` writes a `photo.upserted` event row for `applied`.
- **Data flow**: `PhotoInput` → `UpsertPhoto` (with the `WHERE EXISTS ... OR count < 10` source select in [sqlc/queries.sql](../../../sqlc/queries.sql)) → outcome. `s3_key`, geotag, and `condition` are stored in the `photos` row.
- **How it is tested**: unit, `TestSync_Photo_Applied`, `TestSync_Photo_LimitExceeded`, `TestSync_Photo_IgnoredStale` in [sync_test.go](../../../internal/sync/sync_test.go). Integration (`-tags=integration`), `TestIntegration_FullSyncFlow` fills a site to 10 photos and checks the 11th is `limit_exceeded` and the count stays 10.
- **Read next**: [internal/config/config.go](../../../internal/config/config.go).

## [internal/config/config.go](../../../internal/config/config.go)

- **What it does**: Holds the S3 settings.
- **Control flow**: `Load` reads `S3_ENDPOINT` (no default), `S3_PHOTOS_BUCKET` (default `fieldsync-photos`), `S3_ACCESS_KEY`, `S3_SECRET_KEY` (no default), `AWS_REGION` (default `us-east-1`).
- **Data flow**: env → `Config` → `storage.New`. Empty endpoint and keys mean real S3 and the Lambda role's credentials. Local values come from [.env.example](../../../.env.example) (`S3_ENDPOINT=http://localhost:9000`, MinIO user and password matching [docker-compose.yml](../../../docker-compose.yml)).
- **How it is tested**: untested.
- **Read next**: [docker-compose.yml](../../../docker-compose.yml).

## [docker-compose.yml](../../../docker-compose.yml)

- **What it does**: The `minio` service: image `minio/minio:latest`, command `server /data --console-address ":9001"`, root user `localdevuser`, port 9000 for the S3 API and 9001 for the web console, volume `minio-data`.
- **Control flow**: `make compose-up` starts it; `cmd/server` creates the bucket on boot via `EnsureBucket`. The presigned URL points at `http://localhost:9000/...`, which the browser PUTs to directly.
- **Data flow**: browser PUT → MinIO volume `minio-data`; API `HeadObject` → MinIO.
- **How it is tested**: manual. MILESTONES says `aws s3 ls` against MinIO showed the object, and the downloaded file was a valid 4x4 JPEG.
- **Read next**: [web/src/capture/watermark.ts](../../../web/src/capture/watermark.ts).

## [web/src/capture/watermark.ts](../../../web/src/capture/watermark.ts)

- **What it does**: `watermarkImage(file, label)` draws the photo on a canvas, writes the label on it, and returns a JPEG blob.
- **Control flow**:
  1. `capturePhoto` in [capture.ts](../../../web/src/capture/capture.ts) calls it with the label `lat.toFixed(5), lng.toFixed(5) · <ISO time>`.
  2. `createImageBitmap(file)`, then a canvas of the same width and height, `ctx.drawImage`.
  3. Font size `Math.max(16, Math.round(canvas.width / 30))`; `strokeText` then `fillText` at x = 12, y = height - 12.
  4. `canvas.toBlob(..., 'image/jpeg', 0.9)` wrapped in a Promise; rejects if the blob is null.
- **Data flow**: `File` + label → pixels with text → JPEG `Blob`. The original bytes are replaced; `capturePhoto` stores only the watermarked blob.
- **How it is tested**: untested. MILESTONES says the pipeline was checked by downloading the uploaded object and confirming a valid image.
- **Read next**: [web/src/capture/capture.ts](../../../web/src/capture/capture.ts).

## [web/src/capture/capture.ts](../../../web/src/capture/capture.ts)

- **What it does**: `capturePhoto` is the offline write for a photo.
- **Control flow**:
  1. `CapturePanel.handleCapturePhoto` calls `capturePhoto({siteAssessmentId, file, latitude, longitude, condition})`.
  2. `crypto.randomUUID()` for the id; `watermarkImage(...)` for the blob.
  3. Builds a `PhotoRecord` with `s3Key: ''`.
  4. One Dexie transaction over `db.photos`, `db.photoBlobs`, `db.outbox` adds the metadata row, the blob row, and an outbox row of type `photo`.
- **Data flow**: file + form values → three Dexie rows. No network, no token.
- **How it is tested**: untested.
- **Read next**: [web/src/sync/syncEngine.ts](../../../web/src/sync/syncEngine.ts).

## [web/src/sync/syncEngine.ts](../../../web/src/sync/syncEngine.ts)

- **What it does**: `drainPhotos` uploads queued photos with presign, PUT, confirm.
- **Control flow**:
  1. `drainOutbox` calls `drainRecords` then `drainPhotos`; `drainPhotos` filters outbox entries of type `photo` and gets the ID token once.
  2. Per entry it reads `db.photos` and `db.photoBlobs`. If either is missing it deletes the outbox row and moves on.
  3. `POST .../photos/presign` with `{photoId, contentType: blob.type}`; non-2xx throws.
  4. `PUT uploadUrl` with `Content-Type` and the blob; non-2xx throws.
  5. `POST .../photos/{id}/confirm` with `s3Key`, lat, lng, condition, `createdAt`, `updatedAt`.
  6. If the outcome is `applied`: `db.photos.update(photo.id, { s3Key })` and `db.photoBlobs.delete`. Whatever the outcome, `db.outbox.delete(entry.id)` and update the summary counts.
- **Data flow**: outbox row → Dexie photo + blob → API URL → S3 bytes → confirm outcome → local `s3Key` set, blob deleted (applied only).
- **How it is tested**: untested in code. Manual Playwright run per MILESTONES: offline capture, login, Sync now, all 3 records applied, outbox 0.
- **Read next**: [Phase 4 FILES](../phase-4-dashboard/FILES.md).
