# Phase 1 — Auth: Files

Siblings: [BASICS.md](BASICS.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

> Note: the "E2E verified with Playwright" line in MILESTONES is a one-time manual run. There are no Playwright test files in the repo (`git ls-files` shows none), and `web/package.json` has no test runner. The Go unit tests below are the only committed tests for this phase.

## [internal/auth/auth.go](../../../internal/auth/auth.go)

- **What it does**: Verifies a Cognito ID token against the pool's JWKS and puts `Claims{Sub, Email}` into the request context.
- **Control flow**:
  1. `NewVerifier(issuer, audience)` (called from [cmd/server/main.go](../../../cmd/server/main.go)) builds the JWKS URL as `{issuer}/.well-known/jwks.json` with a 5-minute TTL.
  2. `(*Verifier).Middleware` wraps the next handler and calls `verify(r)`.
  3. `verify` reads `Authorization`, strips `Bearer ` with `strings.CutPrefix`, returns `ErrMissingToken` if absent, else calls `VerifyToken`.
  4. `VerifyToken` calls `keySet(ctx)`. `keySet` takes the mutex and returns the cached set, or calls `jwk.Fetch` and stores it with `fetchedAt`.
  5. `jwt.Parse(..., jwt.WithKeySet(set), jwt.WithIssuer(...), jwt.WithAudience(...))` checks the signature, issuer, audience, and (by default, see the test) expiry.
  6. `tok.Get("token_use", &tokenUse)` must equal `"id"`.
  7. On success `Middleware` stores `Claims` with `context.WithValue(r.Context(), ctxKey{}, claims)`. On any error it writes `401 unauthorized`.
  8. Handlers call `FromContext(ctx)` to read the claims.
- **Data flow**: `Authorization` header string → parsed JWT → `Claims{Sub, Email}` → request context. `Email` stays empty if the token has none. Nothing is persisted.
- **How it is tested**: [auth_test.go](../../../internal/auth/auth_test.go), unit only. A JWKS-failure path and key-rotation path have no test.
- **Read next**: [auth_test.go](../../../internal/auth/auth_test.go).

> Note: the package comment says the middleware "becomes a no-op once API Gateway does the verification in prod". The code does not do that: `NewRouter` always mounts `verifier.Middleware`, and [cmd/lambda/main.go](../../../cmd/lambda/main.go) builds a `Verifier` too. In prod the token would be checked twice (API Gateway authorizer, then this).

## [internal/auth/auth_test.go](../../../internal/auth/auth_test.go)

- **What it does**: Five unit tests for the middleware, using a fake issuer.
- **Control flow**:
  1. `newTestIssuer` generates a 2048-bit RSA key, publishes the public key as a JWKS at `/.well-known/jwks.json` on an `httptest.NewServer`, and returns the URL as the issuer.
  2. `signToken` builds a JWT with the chosen issuer, audience, `token_use`, and `exp`, and signs it with RS256.
  3. `doRequest` runs `v.Middleware(next).ServeHTTP` with an `httptest` request and records the status plus the claims `next` saw.
  4. Each test asserts one rule.
- **Data flow**: test input (claims, expiry) → signed token → middleware → status code and context claims.
- **How it is tested** (the tests themselves, all unit, no `-tags=integration`):
  - `TestMiddleware_ValidToken`: 200, `Sub` is `user-123`, `Email` is the claim.
  - `TestMiddleware_MissingToken`: 401, no claims in context.
  - `TestMiddleware_ExpiredToken`: 401.
  - `TestMiddleware_WrongAudience`: 401.
  - `TestMiddleware_WrongTokenUse`: 401 for `token_use=access`.
- **Read next**: [internal/httpapi/router.go](../../../internal/httpapi/router.go).

## [internal/httpapi/router.go](../../../internal/httpapi/router.go)

- **What it does**: Decides which routes need a token. Phase 1 contributes the `r.Group` with `r.Use(verifier.Middleware)` and `whoamiHandler`.
- **Control flow**:
  1. `NewRouter(verifier, ...)` registers `/health` and `/ws` outside the group.
  2. Inside `r.Group`, `verifier.Middleware` runs before every `/api/*` route.
  3. `GET /api/whoami` → `whoamiHandler` → `auth.FromContext` → JSON `{"sub": ..., "email": ...}`.
- **Data flow**: context claims → JSON reply. `whoamiHandler` ignores the second return value of `FromContext`; it is safe only because the middleware ran first.
- **How it is tested**: no test in `internal/httpapi`. The router wiring is covered only by the manual browser run in MILESTONES.
- **Read next**: [internal/httpapi/ws.go](../../../internal/httpapi/ws.go).

## [internal/httpapi/ws.go](../../../internal/httpapi/ws.go)

- **What it does**: A later-phase WebSocket route that reuses Phase 1's `VerifyToken`.
- **Control flow**:
  1. `wsHandler` parses `locationId` from the query string (400 if invalid).
  2. It calls `verifier.VerifyToken(ctx, r.URL.Query().Get("token"))`. A browser WebSocket cannot set an `Authorization` header, so the token travels in the query string.
  3. On failure it returns 401; on success it accepts the socket and records a connection row.
- **Data flow**: query-param token → `Claims.Sub` → stored as `user_sub` in `ws_connections`.
- **How it is tested**: untested here. Token-in-URL is a trade-off covered in [QUESTIONS.md](QUESTIONS.md).
- **Read next**: [internal/config/config.go](../../../internal/config/config.go).

## [internal/config/config.go](../../../internal/config/config.go)

- **What it does**: Supplies `CognitoIssuer` and `CognitoAudience` to `NewVerifier`.
- **Control flow**:
  1. `Load` reads `COGNITO_ISSUER`.
  2. If unset, it builds `COGNITO_ENDPOINT` (default `http://localhost:9229`) + `/` + `COGNITO_USER_POOL_ID`.
  3. `CognitoAudience` is `COGNITO_CLIENT_ID`.
- **Data flow**: env → issuer URL and audience string. The issuer must equal the `iss` claim exactly, or `jwt.WithIssuer` rejects every token. Terraform sets `COGNITO_ISSUER` directly in prod ([envs/local/main.tf](../../../infra/terraform/envs/local/main.tf)).
- **How it is tested**: untested.

> Note: [.env.example](../../../.env.example) lists `COGNITO_LOCAL_ENDPOINT`, but no Go, script, or TypeScript file reads that name. The seed script writes `COGNITO_ENDPOINT` into `.env.local`.

- **Read next**: [scripts/seed-cognito-local.sh](../../../scripts/seed-cognito-local.sh).

## [scripts/seed-cognito-local.sh](../../../scripts/seed-cognito-local.sh)

- **What it does**: Creates (or reuses) a user pool and app client in `cognito-local`, then writes `.env.local`.
- **Control flow**:
  1. Sets fake AWS creds and `ENDPOINT=http://localhost:9229`.
  2. `list-user-pools` looks for `fieldsync-local`; creates it only if missing.
  3. `list-user-pool-clients` looks for `fieldsync-web`; creates it with `--no-generate-secret` and `ALLOW_USER_PASSWORD_AUTH ALLOW_REFRESH_TOKEN_AUTH`.
  4. Writes `.env.local` with `VITE_*` (for the browser) and unprefixed `COGNITO_*` (for Go) variables, plus `VITE_API_URL`.
- **Data flow**: cognito-local state → ids → `.env.local` (gitignored). Needs the `aws` CLI installed (the script calls `aws` directly).
- **How it is tested**: untested by code. MILESTONES says it was verified idempotent by running it twice.
- **Read next**: [web/src/amplifyConfig.ts](../../../web/src/amplifyConfig.ts).

## [web/src/amplifyConfig.ts](../../../web/src/amplifyConfig.ts)

- **What it does**: Points Amplify Auth at `cognito-local`.
- **Control flow**:
  1. [web/src/main.tsx](../../../web/src/main.tsx) imports `./amplifyConfig` before rendering, so the side-effect `Amplify.configure(...)` runs first.
  2. It passes `userPoolId`, `userPoolClientId`, and `userPoolEndpoint` from `import.meta.env.VITE_*`.
- **Data flow**: `.env.local` (`VITE_COGNITO_*`) → Amplify config. In prod the custom endpoint is not meant to be used; I did not find a separate prod config in `web/src`, so treat prod web config as not covered here.
- **How it is tested**: untested.
- **Read next**: [web/src/auth/AuthPanel.tsx](../../../web/src/auth/AuthPanel.tsx).

## [web/src/auth/AuthPanel.tsx](../../../web/src/auth/AuthPanel.tsx)

- **What it does**: A small form: register, confirm, login, sign out, and a "Call /api/whoami" button. It is Phase 1's proof that browser, Cognito, and Go agree.
- **Control flow**:
  1. `handleSignUp` calls Amplify `signUp` then switches to the confirm mode.
  2. `handleConfirm` calls `confirmSignUp` with the code (locally `123456`).
  3. `handleSignIn` calls `signIn({ ..., options: { authFlowType: 'USER_PASSWORD_AUTH' } })`.
  4. `handleCallWhoami` calls `fetchAuthSession()`, takes `session.tokens?.idToken`, and `fetch`es `${backendUrl}/api/whoami` with `Authorization: Bearer <idToken>`.
- **Data flow**: form fields → Amplify → tokens held by Amplify → `Authorization` header → server `Claims` → JSON `{sub, email}` shown on screen.
- **How it is tested**: untested in code. MILESTONES records a manual Playwright run (register, confirm, login, whoami, zero console errors).
- **Read next**: [Phase 2 FILES](../phase-2-sync-api-db/FILES.md).
