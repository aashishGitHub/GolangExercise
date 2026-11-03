# Phase 3 — Photos / S3: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

Phase 3 adds photos. A photo is big, so it does not go through the API. The phone asks the API for a one-time upload link, sends the bytes straight to storage, then tells the API "it is there".

1. **Object storage and S3** — S3 is AWS's file store. You put a file (an object) in a bucket under a name (a key). It is not a database and has no folders, only long key names. Think of a warehouse where each box has a label. In this project: [internal/storage/s3.go](../../../internal/storage/s3.go).

2. **Bucket, object, key** — A bucket is the top-level container. An object is the file. The key is its full name, like `photos/<siteId>/<photoId>`. Think of bucket = warehouse, key = shelf label. In this project: `key := fmt.Sprintf("photos/%s/%s", siteID, req.PhotoID)` in [internal/httpapi/photos.go](../../../internal/httpapi/photos.go).

3. **Direct upload** — The phone sends the file to S3 itself. The API server never touches the bytes. This keeps the API small and avoids payload limits of the Lambda it runs on. Think of a courier collecting the parcel from the shop, not routing it via the head office. In this project: `fetch(uploadUrl, { method: 'PUT', ... })` in [web/src/sync/syncEngine.ts](../../../web/src/sync/syncEngine.ts).

4. **Presigned URL** — A presigned URL is a normal S3 link with a signature baked in. It lets the holder do one specific thing (here, PUT one key) until it expires, with no login. Think of a valet ticket that works for one car and one afternoon. In this project: `PresignPut` in [s3.go](../../../internal/storage/s3.go), 15 minutes (`presignExpiry`) in [photos.go](../../../internal/httpapi/photos.go).

5. **Who signs it** — The server signs the URL with its own AWS keys. The phone never sees those keys. If someone changes the key name or content type in the URL, the signature no longer matches. Think of a sealed envelope: you can carry it, not edit it. In this project: `PresignPutObject` with `Bucket`, `Key`, `ContentType` in [s3.go](../../../internal/storage/s3.go).

6. **Three-step upload (presign, PUT, confirm)** — Step 1: ask the API for a link. Step 2: PUT the bytes to S3. Step 3: tell the API to save the photo row. The API only writes the database row after step 3. Think of paying, collecting, then signing the receipt. In this project: `presignHandler` and `confirmHandler` in [photos.go](../../../internal/httpapi/photos.go).

7. **Why confirm?** — The API cannot see the PUT, because it went to S3. Without a confirm step the database could list a photo that never arrived. Confirm asks S3 "is it really there?" before saving. Think of checking the parcel is on the shelf before writing it in the ledger. In this project: `store.Exists` in [confirmHandler](../../../internal/httpapi/photos.go).

8. **HEAD request** — HEAD is like GET but returns only the headers, no body. It is a cheap "does this exist and how big?" check. Think of reading the label without opening the box. In this project: `HeadObject` in `Exists` ([s3.go](../../../internal/storage/s3.go)).

9. **409 Conflict** — The HTTP status for "the request does not fit the current state". Confirm returns 409 if the object is not in storage yet. Think of a clerk saying "you cannot sign for a parcel that has not arrived". In this project: `http.StatusConflict` in [photos.go](../../../internal/httpapi/photos.go).

10. **MinIO** — MinIO is a free program that speaks the S3 protocol. It stands in for S3 on your laptop. Think of a practice kitchen with the same layout as the real one. In this project: the `minio` service in [docker-compose.yml](../../../docker-compose.yml), API on port 9000.

11. **Custom endpoint** — The S3 client normally talks to AWS. A custom endpoint tells it to talk to another address, like `http://localhost:9000`. Think of changing the address on the envelope. In this project: `o.BaseEndpoint = aws.String(endpoint)` in `storage.New` ([s3.go](../../../internal/storage/s3.go)).

12. **Path-style addressing** — Two ways to name a bucket in a URL. Virtual-hosted: `bucket.host/key`. Path-style: `host/bucket/key`. The code comment says MinIO needs path-style, so the client turns it on whenever a custom endpoint is set. Think of "Bob's Shop, 5 Main St" versus "5 Main St, Bob's Shop". In this project: `o.UsePathStyle = true` when an endpoint is set ([s3.go](../../../internal/storage/s3.go)).

13. **Credentials chain** — The AWS SDK looks for keys in a fixed order (env vars, config files, the role of the running Lambda). If you pass static keys, it uses those. Prod passes none, so the Lambda's role is used. Think of a keyring: use the key you were handed, else the master key. In this project: `config.WithCredentialsProvider(...)` only when both keys are set ([s3.go](../../../internal/storage/s3.go)).

14. **Least privilege (IAM)** — Give a program only the permissions it needs. The Lambda role's S3 permissions are only `s3:PutObject` and `s3:GetObject` on the photos bucket's objects (plus `secretsmanager:GetSecretValue` on the one DB secret). Think of a hotel key card that opens only your floor. In this project: `aws_iam_role_policy.lambda_app` in [infra/terraform/modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf).

15. **EnsureBucket** — A function that creates the bucket if it is missing. Locally nobody runs Terraform, so the server makes the bucket at startup. Prod does not call it. Think of setting the table before guests arrive. In this project: `store.EnsureBucket(ctx)` in [cmd/server/main.go](../../../cmd/server/main.go).

16. **Blob** — A blob is raw bytes with a type, like an image file in memory. Think of a sealed box whose contents the browser does not inspect. In this project: the `blob: Blob` field in `PhotoBlobRecord` ([web/src/db/schema.ts](../../../web/src/db/schema.ts)).

17. **Why photo bytes live in their own Dexie table** — Photo metadata (id, condition, lat, lng) is small. Image bytes are big. Keeping the bytes in `photoBlobs` means listing photos never loads images into memory. Think of a library catalog separate from the books. In this project: the `photoBlobs` table in [schema.ts](../../../web/src/db/schema.ts).

18. **Canvas and watermark** — A canvas is a drawing surface in the browser. The app draws the photo on it, then writes the geotag and time on top, then saves it as a JPEG. The proof of place and time is burned into the pixels. Think of stamping a date on a printed photo. In this project: `watermarkImage` in [web/src/capture/watermark.ts](../../../web/src/capture/watermark.ts).

19. **Geotag** — A geotag is a latitude and longitude attached to a photo. In this code the numbers come from two text fields in the UI, not the phone's GPS. Think of writing the address on the back by hand. In this project: `latitude: Number(lat)` in [CapturePanel.tsx](../../../web/src/capture/CapturePanel.tsx).

20. **Condition** — Each photo is labeled `good`, `moderate`, or `bad`. The database rejects any other value. Think of a traffic light with only three colors. In this project: `Condition` in [schema.ts](../../../web/src/db/schema.ts), `CHECK` in [000001_init_schema.up.sql](../../../migrations/000001_init_schema.up.sql).

21. **Reuse, not duplicate** — Confirm does not have its own insert logic. It calls the same `Sync` code as `/api/sync`, so last-write-wins, idempotency, and the 10-photo cap are written once. Think of one till for the whole shop. In this project: `svc.Sync(...)` inside `confirmHandler` ([photos.go](../../../internal/httpapi/photos.go)) and `applyPhoto` in [internal/sync/sync.go](../../../internal/sync/sync.go).

22. **Emulator swap** — The plan used LocalStack for S3. LocalStack refused to start without an auth token, so the project switched to MinIO. The Go code did not change, only the endpoint setting. Think of replacing a rented practice room with a free one that has the same piano. In this project: comment above `minio` in [docker-compose.yml](../../../docker-compose.yml).

23. **Idempotent retry of an upload** — The S3 key is fixed by site id and photo id. If the PUT worked but the confirm failed, the next try asks for the same key and uploads the same bytes again. It overwrites, it does not duplicate. Think of re-posting the same letter to the same address. In this project: `key := fmt.Sprintf(...)` in [photos.go](../../../internal/httpapi/photos.go) and `drainPhotos` in [syncEngine.ts](../../../web/src/sync/syncEngine.ts).
