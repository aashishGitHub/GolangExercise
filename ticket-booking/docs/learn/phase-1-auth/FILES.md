# Phase 1 — Auth: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

Scope: MILESTONES Phase 1 is backend only (verifier, seed script, `/whoami`, 5 unit tests). The browser sign-in UI came later (Phase 4 E2E, Phase 13 UX repair per [plan.md](../../plan.md)). Those web files are listed last, marked as later layers.

## [internal/auth/auth.go](../../../internal/auth/auth.go)

- **What it does**: verifies a Cognito ID token (JWKS fetch, RS256 signature, issuer, audience, `token_use=id`) and exposes `sub` and `email` through the request context.
- **Control flow**:
  1. `wiring.New` calls `auth.NewVerifier(cfg.CognitoIssuerURL(), cfg.CognitoAudience)`.
  2. A request to a gated route reaches `Verifier.Middleware`.
  3. `Middleware` calls `v.verify(r)`, which takes the header, strips `Bearer ` with `strings.CutPrefix`, and returns `ErrMissingToken` if absent or empty.
  4. `verify` calls `VerifyToken(ctx, tokenStr)`.
  5. `VerifyToken` calls `keySet(ctx)`: under a mutex, return the cached `jwk.Set` if younger than `ttl` (5 minutes), else `jwk.Fetch` from `<issuer>/.well-known/jwks.json`.
  6. `jwt.Parse` with `WithKeySet`, `WithIssuer`, `WithAudience` checks signature, issuer, audience, and expiry. Any failure wraps `ErrInvalidClaims`.
  7. `tok.Get("token_use", ...)` must equal `"id"`.
  8. On success `Middleware` stores `Claims` in the context under an unexported `ctxKey{}`. On any error it writes 401 `unauthorized` and stops.
  9. Handlers call `auth.FromContext(ctx)`.
- **Data flow**: `Authorization` header string → token string → parsed JWT + key set → `Claims{Sub, Email}` → request context → handler. Nothing is persisted. The only state is the in-memory key set and its `fetchedAt` time.
- **How it is tested**: unit, no build tag, in [internal/auth/auth_test.go](../../../internal/auth/auth_test.go) (next section). `VerifyToken` is also called by `wshub` ([internal/wshub/wshub.go](../../../internal/wshub/wshub.go)) with the token from a query string, and that path has no test in this phase.
- **Read next**: [internal/auth/auth_test.go](../../../internal/auth/auth_test.go).

## [internal/auth/auth_test.go](../../../internal/auth/auth_test.go)

- **What it does**: five unit tests of the middleware against a self-signed JWKS server.
- **Control flow**:
  1. `newTestIssuer` generates a 2048-bit RSA key, builds a `jwk.Set` with `kid` `test-key` and `alg` RS256, and serves it from an `httptest.Server` at `/.well-known/jwks.json`.
  2. `signToken` builds a JWT (issuer, audience, subject `user-123`, expiry, `token_use`, extra claims) and signs it with RS256.
  3. `doRequest` wraps a tiny handler with `v.Middleware`, sends a request with the given `Authorization` header via `httptest`, and returns status plus any claims the handler saw.
  4. Each test calls these three helpers with one variation.
- **Data flow**: test-generated key → signed token → middleware → status code and `Claims`. No Docker, no network beyond loopback.
- **How it is tested**: this is the test. Tests: `TestMiddleware_ValidToken` (200, `Sub` = `user-123`, email present), `TestMiddleware_MissingToken` (401), `TestMiddleware_ExpiredToken` (401), `TestMiddleware_WrongAudience` (401), `TestMiddleware_WrongTokenUse` (401 for `access`). Run with `go test ./internal/auth/...`.
- **Not covered**: wrong issuer, a tampered signature, JWKS cache expiry or refresh, JWKS fetch failure, `VerifyToken` with an empty string, and `handleWhoami`. MILESTONES verified the tampered-token 401 by hand with `curl`, not in a test.
- **Read next**: [internal/httpapi/router.go](../../../internal/httpapi/router.go).

## [internal/httpapi/router.go](../../../internal/httpapi/router.go)

- **What it does**: decides which routes sit behind the verifier, and serves `GET /api/v1/whoami`.
- **Control flow**:
  1. `NewRouter` receives the `*auth.Verifier`.
  2. Open routes (`/health`, `/api/v1/events...`, `/api/v1/venues/{venueID}/layout`) are registered directly.
  3. `api.Group(func(authed chi.Router) { authed.Use(verifier.Middleware) ... })` wraps `whoami`, queue, holds, orders, and ticket QR routes.
  4. `handleWhoami` calls `auth.FromContext`. If no claims it writes 401. Otherwise it encodes `{"sub":..., "email":...}`.
- **Data flow**: request → CORS and other middleware → `verifier.Middleware` → `handleWhoami` → JSON of the claims.
- **How it is tested**: `handleWhoami` and the route grouping have no Go test. MILESTONES verified with `curl` (valid token returns claims, no header returns 401, tampered returns 401, `/health` stays open). The Playwright E2E ([web/e2e/hold-flow.spec.ts](../../../web/e2e/hold-flow.spec.ts)) later exercises authenticated routes end to end.
- **Read next**: [scripts/seed-cognito-local.sh](../../../scripts/seed-cognito-local.sh).

> Note: the `/api/v1` group now also holds routes added in later phases. `/gate/redeem` and `/ws` sit outside `/api/v1` and are not gated by the verifier middleware (`/ws` authenticates through `VerifyToken` inside `wshub`).

## [scripts/seed-cognito-local.sh](../../../scripts/seed-cognito-local.sh)

- **What it does**: creates (or reuses) a user pool named `ticketing-local` and an app client `ticketing-web` in cognito-local, then writes their IDs to `.env.local`.
- **Control flow**:
  1. Sets dummy AWS credentials and `ENDPOINT=http://localhost:9229`.
  2. `list-user-pools` and a JMESPath query (AWS CLI's filter language) look for the pool by name. If found, reuse. Else `create-user-pool`.
  3. Same pattern for the client with `list-user-pool-clients` / `create-user-pool-client` using `ALLOW_USER_PASSWORD_AUTH` and `ALLOW_REFRESH_TOKEN_AUTH` and `--no-generate-secret`.
  4. Writes `VITE_COGNITO_*`, `VITE_API_URL`, and `COGNITO_ENDPOINT`, `COGNITO_USER_POOL_ID`, `COGNITO_CLIENT_ID` to `.env.local`.
- **Data flow**: cognito-local state → pool ID and client ID → `.env.local` → the Makefile's `LOAD_ENV` → `config.Load()` (`CognitoPoolID`, `CognitoAudience`) and the Vite app (`VITE_` names).
- **How it is tested**: not by a test file. Verified by hand in MILESTONES (pool and client created, then sign-up, confirm, `initiate-auth`, `whoami`).
- **Read next**: [web/src/auth/useAuth.ts](../../../web/src/auth/useAuth.ts).

## [web/src/auth/useAuth.ts](../../../web/src/auth/useAuth.ts) (later layer)

- **What it does**: React hook that wraps Amplify Auth: session restore on mount, `register`, `confirm`, `login`, `logout`, and `idToken()` for API calls. [web/src/amplifyConfig.ts](../../../web/src/amplifyConfig.ts) points Amplify at cognito-local through `VITE_COGNITO_*` variables.
- **Control flow**:
  1. `App` imports `./amplifyConfig` (runs `Amplify.configure`) and calls `useAuth()`.
  2. On mount an effect calls `fetchAuthSession()`. If an ID token exists, `getCurrentUser()` then status `authed`, else `anon`. Until then status is `loading`.
  3. `login` calls `signIn` with `authFlowType: 'USER_PASSWORD_AUTH'`.
  4. `EventPage` calls `idToken()` before each API call, and passes the string as `token` to `apiFetch` in [web/src/api/client.ts](../../../web/src/api/client.ts), which sets `Authorization: Bearer ...`.
  5. Errors go through `describeAuthError` in [web/src/auth/authErrorCopy.ts](../../../web/src/auth/authErrorCopy.ts), which maps Cognito exception names to plain messages.
- **Data flow**: user input → Amplify → cognito-local → ID token (kept by Amplify) → `idToken()` → `Authorization` header → Go verifier.
- **How it is tested**: no unit test for `useAuth`. The E2E tests in [web/e2e/hold-flow.spec.ts](../../../web/e2e/hold-flow.spec.ts) (next section) drive it for real.
- **Read next**: [web/e2e/hold-flow.spec.ts](../../../web/e2e/hold-flow.spec.ts).

## [web/e2e/hold-flow.spec.ts](../../../web/e2e/hold-flow.spec.ts) (later layer)

- **What it does**: Playwright browser tests against the full running stack.
- **Control flow**: test 1, `browses signed out, then creates an account, confirms, signs in, and reserves a seat`, registers a unique email, confirms with the prefilled `123456` code, signs in, picks a seat by keyboard, reserves, and releases. Test 2, `prompts sign-in when reserving while signed out, and preserves the selection through it`, checks that the reserve action resumes after sign-in.
- **Data flow**: real browser → Vite dev server (`localhost:5173`) → Go server → cognito-local, Postgres, Redis.
- **How it is tested**: E2E (needs the stack, a seeded venue, and event 1). A `beforeEach` listener collects console errors and asserts none on page close (unverified that this fails the test; see the Phase 4 FILES note). CI does not run it (see [ticketing-ci.yml](../../../../.github/workflows/ticketing-ci.yml)).
- **Read next**: [Phase 2 FILES](../phase-2-catalog-seatmap/FILES.md).
