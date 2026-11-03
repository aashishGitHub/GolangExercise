# Phase 1 — Auth: Questions

Siblings: [BASICS.md](BASICS.md) · [FILES.md](FILES.md) · [learn index](../README.md)

## L1 Beginner

**Q1. What is a JWT and why does the API need one?**
A JWT is a signed string carrying claims (`sub`, `email`, `iss`, `aud`, `exp`). The API cannot trust a client that just says "I am user 5". It trusts a token signed by Cognito that it can check. See `VerifyToken` in [internal/auth/auth.go](../../../internal/auth/auth.go).

**Q2. What is the difference between 401 and 403?**
401: caller not identified (no, bad, or expired token). 403: identified but not allowed. This middleware only does identity, so it only returns 401 (`http.StatusUnauthorized`) in [internal/auth/auth.go](../../../internal/auth/auth.go).

**Q3. Which routes need a token and which do not?**
Routes inside the `authed` group in [internal/httpapi/router.go](../../../internal/httpapi/router.go) need one: `whoami`, queue, holds, orders, ticket QR. `/health` and the catalog, availability, pricing, and layout routes are open.

Try it:
```
curl -s -o /dev/null -w "%{http_code}\n" localhost:8080/api/v1/whoami
```

**Q4. Why does the project run cognito-local?**
To have a login service that issues real RS256-signed tokens and a JWKS endpoint with no AWS account. See `cognito-local` in [docker-compose.yml](../../../docker-compose.yml).

## L2 Junior

**Q5. Trace a request to `/api/v1/whoami` through the code.**
`NewRouter` puts `whoami` in the `authed` group ([internal/httpapi/router.go](../../../internal/httpapi/router.go)). `Verifier.Middleware` calls `verify`, which extracts the bearer token and calls `VerifyToken`. That calls `keySet` (cached JWKS), `jwt.Parse` with issuer and audience options, then checks `token_use == "id"`. On success the `Claims` go into the context and `handleWhoami` reads them with `auth.FromContext`. All in [internal/auth/auth.go](../../../internal/auth/auth.go).

**Q6. How does the verifier get the public key, and how often?**
`keySet` fetches `<issuer>/.well-known/jwks.json` with `jwk.Fetch` and caches the set for `ttl` = 5 minutes under a mutex. See `NewVerifier` and `keySet` in [internal/auth/auth.go](../../../internal/auth/auth.go).

**Q7. How does a handler know who the caller is?**
Middleware stores `Claims` with `context.WithValue` under the private key type `ctxKey`. Handlers call `auth.FromContext`, for example in [internal/httpapi/holds.go](../../../internal/httpapi/holds.go). A private key type means no other package can overwrite it by accident.

**Q8. How do you get a token locally to call the API?**
Run the seed script, sign up, confirm with code `123456`, then `initiate-auth` with `USER_PASSWORD_AUTH`. The `IdToken` goes in the `Authorization: Bearer` header. Use the ID token, not the access token, or `token_use` fails.

Try it:
```
bash scripts/seed-cognito-local.sh
```

## L3 Mid

**Q9. Why verify JWTs in Go instead of relying on API Gateway?**
In prod, API Gateway's JWT authorizer does this and the Go middleware becomes redundant (package comment in [internal/auth/auth.go](../../../internal/auth/auth.go)). Locally there is no API Gateway, so the same checks run in-process. One code path then behaves the same in both places. The Terraform `api` module has the JWT authorizer per [plan.md](../../plan.md). I did not verify the module's exact settings here.

**Q10. Why do the unit tests use a self-signed JWKS server rather than cognito-local?**
Unit tests then need no Docker and run in CI with plain `go test ./...`. `newTestIssuer` in [internal/auth/auth_test.go](../../../internal/auth/auth_test.go) generates a key and serves its JWKS from `httptest`. The cost: it proves the verifier logic, not that cognito-local's real tokens and JWKS have the shape the code expects. That was covered by the manual `curl` run in [MILESTONES.md](../../../MILESTONES.md).

**Q11. What do the five tests prove, and what do they miss?**
They prove: a valid ID token yields 200 with the right `sub` and `email`; missing, expired, wrong-audience, and wrong-`token_use` tokens yield 401. They do not test: wrong issuer, bad signature, JWKS cache expiry, JWKS fetch failure, or `handleWhoami`. See the test list in [internal/auth/auth_test.go](../../../internal/auth/auth_test.go).

**Q12. Why does the seed script set `ALLOW_USER_PASSWORD_AUTH`?**
cognito-local does not implement SRP, so the app client allows the plain password flow and `useAuth.login` asks for `USER_PASSWORD_AUTH` ([web/src/auth/useAuth.ts](../../../web/src/auth/useAuth.ts)). plan.md says the real Terraform `auth` module sets the same flow so the real pool matches. Real Cognito would normally use SRP, which is a fidelity gap, listed in [plan.md](../../plan.md) risks.

## L4 Senior

**Q13. What happens if the JWKS endpoint is down or the issuer rotates keys?**
`keySet` returns an error when `jwk.Fetch` fails and no cached set is still valid, and `Middleware` turns every error into 401. An infrastructure outage is therefore indistinguishable from a bad token for the client. A key rotation is not handled specially: there is no refresh on unknown `kid`, so a token signed by a new key fails until the 5-minute TTL expires. Both are read from [internal/auth/auth.go](../../../internal/auth/auth.go); I did not test either.

**Q14. Is there a concurrency problem in `keySet`?**
It holds one mutex for the whole call, including the network `jwk.Fetch`. During a refresh, every concurrent request waits. It is correct, and simple, but slow under a cold cache or slow issuer. Fix options (singleflight, background refresh) are not in the code. I have not measured this.

**Q15. What is not solved by this phase?**
Authorization: the middleware proves identity only. Ownership checks (can this user release this hold?) live in the handlers and inventory layer, built in later phases. Also not solved: token revocation (a stolen token is valid until `exp`), and a real SRP flow. Cognito claims are a subset of real Cognito's, per [plan.md](../../plan.md) risk 7 on the fidelity list.

**Q16. At 100x traffic, what changes?**
Verification is local CPU work plus one cached key set, so it scales with instances, not with the issuer. Each instance keeps its own cache, so JWKS fetches grow with instance count (not measured here). In prod the API Gateway authorizer would take this off the Go servers entirely, which is why the package is designed to become a no-op there. The WebSocket path is different: `wshub` calls `VerifyToken` from a query-string token ([internal/wshub/wshub.go](../../../internal/wshub/wshub.go)), so that path still needs the Go verifier or an equivalent authorizer.
