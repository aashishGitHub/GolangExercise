# Phase 1 — Auth: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [../README.md](../README.md)

## L1 Beginner

**Q1. What is a JWT, and what is inside it?**
A JWT is a signed string of three dot-separated parts: header (algorithm, key id), claims (facts such as `sub`, `email`, `iss`, `aud`, `exp`, `token_use`), and signature. Claims are readable by anyone; the signature proves the issuer made them. The server trusts the claims only after verifying the signature. Source: `VerifyToken` in [auth.go](../../../internal/auth/auth.go).

**Q2. Why use Cognito instead of a users table in our own database?**
The app never stores or checks passwords; Cognito does sign-up, confirmation, and login, and the Go server only verifies the token Cognito signed. Locally `cognito-local` plays the same role. Source: [scripts/seed-cognito-local.sh](../../../scripts/seed-cognito-local.sh), [infra/terraform/modules/auth/main.tf](../../../infra/terraform/modules/auth/main.tf).

**Q3. What does "auth gates sync, not capture" mean?**
A field worker offline cannot log in, but must still record data. Capture writes only to the phone (Dexie), with no token. Only sync (sending to `/api/sync`) needs a valid token. In code: `verifier.Middleware` wraps only the `/api` group, and [capture.ts](../../../web/src/capture/capture.ts) never touches the network. Source: [router.go](../../../internal/httpapi/router.go).

## L2 Junior

**Q4. Trace `GET /api/whoami` from the browser to the JSON reply.**
1. [AuthPanel.tsx](../../../web/src/auth/AuthPanel.tsx) `handleCallWhoami`: `fetchAuthSession()`, take the ID token, send `Authorization: Bearer <token>`.
2. chi global middleware runs, then the `/api` group's `verifier.Middleware` ([auth.go](../../../internal/auth/auth.go)).
3. `verify` strips `Bearer `; `VerifyToken` gets the key set, calls `jwt.Parse` with issuer and audience, and checks `token_use == "id"`.
4. On success `Claims` goes into the context; `whoamiHandler` ([router.go](../../../internal/httpapi/router.go)) reads it with `auth.FromContext` and writes `{"sub":..., "email":...}`.
5. On any failure the middleware writes `401 unauthorized` and the handler never runs.

Try it:
```
curl -i localhost:8080/api/whoami
```
(no token, so expect `401`)

**Q5. Where does the server get the public key, and how often?**
From `{issuer}/.well-known/jwks.json` via `jwk.Fetch` in `keySet`. The result is cached in the `Verifier` for 5 minutes (`ttl`), guarded by a mutex. Source: [auth.go](../../../internal/auth/auth.go).

Try it:
```
cat .env.local
```
(find `COGNITO_USER_POOL_ID`, then open `http://localhost:9229/<pool id>/.well-known/jwks.json` in a browser)

**Q6. How does a local user get a token?**
Register (`signUp`), confirm with the fixed code `123456` (`confirmSignUp`), then `signIn` with `authFlowType: 'USER_PASSWORD_AUTH'`. Amplify is pointed at `cognito-local` by [amplifyConfig.ts](../../../web/src/amplifyConfig.ts) using `VITE_COGNITO_*` values the seed script wrote.

Try it:
```
./scripts/seed-cognito-local.sh
```

## L3 Mid

**Q7. Why verify the JWT in Go when API Gateway can do it in prod?**
Local dev has no API Gateway. A Go middleware gives the same check in both runtimes, and [auth_test.go](../../../internal/auth/auth_test.go) can test it. Cost: in prod the token is verified twice (the Terraform authorizer in [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf), then `verifier.Middleware` inside the Lambda). The package comment claims it "becomes a no-op" in prod; the code does not do that.

**Q8. What do the five unit tests prove, and what do they not?**
`newTestIssuer` serves a real JWKS from `httptest`, and `signToken` signs real RS256 tokens, so the whole parse-verify path runs with no mocks. They prove: valid token passes with the right `Sub` and `Email`; missing token, expired token, wrong audience, and `token_use=access` all give 401. They do not cover: wrong issuer, bad signature, JWKS unreachable, cache expiry, key rotation, or a real Cognito/cognito-local token. Source: [auth_test.go](../../../internal/auth/auth_test.go).

Try it:
```
go test ./internal/auth/...
```

**Q9. Why accept only `token_use=id`, and why `USER_PASSWORD_AUTH`?**
The server reads `email` from the token, and ID tokens carry user attributes (test comment in `TestMiddleware_WrongTokenUse` says access tokens do not). I am not certain which exact claims Cognito puts in an access token; check the Cognito docs before relying on this. For the login flow, `cognito-local` does not support SRP, so the seed script allows `ALLOW_USER_PASSWORD_AUTH` and the UI requests it; the Terraform pool also allows SRP for real Cognito (comment in [modules/auth/main.tf](../../../infra/terraform/modules/auth/main.tf)). Sending the password directly is acceptable locally only.

## L4 Senior

**Q10. What happens when the JWKS endpoint is down or the key rotates?**
`keySet` returns an error, `verify` returns it, and `Middleware` answers 401 for every request. An outage looks like "everyone is logged out", not 503. Also `keySet` holds the mutex during the network fetch, so concurrent requests wait behind one fetch. I did not find code that refetches when a token's key id is not in the cached set, so after a rotation I expect failures until the 5-minute TTL passes. Verify against `jwx` behavior before relying on this. Source: [auth.go](../../../internal/auth/auth.go).

**Q11. What is the risk of the token in the WebSocket query string?**
`wsHandler` reads `token` from `r.URL.Query()` because browsers cannot set headers on a WebSocket. URLs tend to land in logs and proxies. `middleware.Logger` is installed globally in `NewRouter`; I believe chi's logger records the request URI, which would include the token, but verify that in chi's source. The comment says real API Gateway WebSocket JWT authorizers are commonly wired the same way. Source: [ws.go](../../../internal/httpapi/ws.go), [router.go](../../../internal/httpapi/router.go).

**Q12. What does this phase not solve?**
- No revocation: a signed token is valid until `exp`; there is no denylist.
- No authorization beyond "logged in": `getLocationHandler` and `getSiteHandler` in [internal/httpapi](../../../internal/httpapi/locations.go) do not compare the caller's `sub` to `created_by`, so any signed-in user can read any record by id.
- No server-side refresh or session logic: Amplify owns it in the browser.
- Local tokens come from `cognito-local`, which pins `4.0.0` and hardcodes port 9229 in returned URLs (comment in [docker-compose.yml](../../../docker-compose.yml)); real Cognito behavior is untested here.

**Q13. What breaks at 100x traffic?**
Each instance has its own 5-minute JWKS cache, so JWKS load scales with instance count, not request count; that part is cheap. Each request still pays one RSA signature verify, which is CPU on the API. The lock around `keySet` is held only for the cache read (and during a refetch), so the cost shows up at refresh time. Moving verification to the API Gateway authorizer alone would remove the CPU from the Lambda, but then local dev loses parity (see Q7). I have no benchmark for any of this; the repo has none.
