# Phase 1 — Auth: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

Phase 1 answers one question for every protected request: who is calling, and can we prove it? The proof is a signed token from Cognito (a login service).

1. **Authentication** — Authentication means proving who you are. It is different from authorization, which decides what you may do. This phase does only the first. It is like showing your ID at the door: the guard learns your name but not yet which rooms you may enter.
   In this project: `Verifier.Middleware` in [internal/auth/auth.go](../../../internal/auth/auth.go)

2. **Cognito user pool** — A user pool is AWS's service that stores users and signs them in. Locally the project uses a fake called cognito-local. It is like a club's membership office that issues membership cards.
   In this project: service `cognito-local` in [docker-compose.yml](../../../docker-compose.yml)

3. **App client** — An app client is a registration inside the pool for one application, here the web app. Its client ID later appears inside the token as the audience. It is like the club giving your app its own door number.
   In this project: `create-user-pool-client` in [scripts/seed-cognito-local.sh](../../../scripts/seed-cognito-local.sh)

4. **JWT (JSON Web Token)** — A JWT is a short text string with three parts: a header, a payload of facts called claims, and a signature. Anyone can read it, but only the issuer can sign it. It is like a wristband at a festival with a hologram you cannot fake.
   In this project: parsed with `jwt.Parse` in `VerifyToken`, [internal/auth/auth.go](../../../internal/auth/auth.go)

5. **Claim** — A claim is one fact inside the token, such as `sub` (the user's unique ID), `email`, `iss` (issuer), `aud` (audience), `exp` (expiry time). It is like one line on the wristband: name, date, gate.
   In this project: `Claims{Sub, Email}` in [internal/auth/auth.go](../../../internal/auth/auth.go)

6. **Issuer (`iss`)** — The issuer is the address of the service that created the token. The server accepts only tokens whose issuer matches its configured one. It is like checking the wristband came from our festival, not another one.
   In this project: `jwt.WithIssuer(v.issuer)` in [internal/auth/auth.go](../../../internal/auth/auth.go)

7. **Audience (`aud`)** — The audience says which app the token was made for. A token for app A is rejected by app B. It is like a ticket marked "Hall 2 only".
   In this project: `jwt.WithAudience(v.audience)` and test `TestMiddleware_WrongAudience` in [internal/auth/auth_test.go](../../../internal/auth/auth_test.go)

8. **Expiry (`exp`)** — Every token has an end time. After it, the token is refused even if the signature is perfect. It is like milk with a date on the carton.
   In this project: `TestMiddleware_ExpiredToken` in [internal/auth/auth_test.go](../../../internal/auth/auth_test.go)

9. **ID token vs access token (`token_use`)** — Cognito issues several tokens. The ID token carries who the user is (email, sub). The access token says what the app may call. This server accepts only `token_use=id`. It is like accepting a passport but not a library card.
   In this project: the `token_use` check in `VerifyToken`, [internal/auth/auth.go](../../../internal/auth/auth.go)

10. **Signature and RS256** — RS256 signs a token with a private key held only by the issuer. Anyone with the matching public key can check the signature, but cannot make a new one. It is like a wax seal: everyone can see it is genuine, only the king owns the stamp.
    In this project: `jwa.RS256()` in [internal/auth/auth_test.go](../../../internal/auth/auth_test.go) (test signing); verification uses the key set in [internal/auth/auth.go](../../../internal/auth/auth.go)

11. **JWKS (JSON Web Key Set)** — A JWKS is a public web page listing the issuer's public keys. The server downloads it to verify signatures. Each key has an ID called `kid`. It is like a public notice board showing the king's stamp pattern.
    In this project: `jwksURL` in `NewVerifier`, [internal/auth/auth.go](../../../internal/auth/auth.go)

12. **Cache with TTL** — A cache keeps a copy so you do not fetch it every time. TTL (time to live) is how long the copy is trusted. Here the key set is kept for 5 minutes. It is like writing a phone number on a sticky note instead of calling directory enquiries every time.
    In this project: `keySet` and field `ttl` in [internal/auth/auth.go](../../../internal/auth/auth.go)

13. **Bearer token** — "Bearer" means whoever holds the token is treated as the user. The client sends it in the header `Authorization: Bearer <token>`. It is like a cinema ticket: whoever holds it gets in, so guard it.
    In this project: `verify` in [internal/auth/auth.go](../../../internal/auth/auth.go)

14. **401 Unauthorized** — HTTP status 401 means "I do not know who you are". Missing, expired, or tampered tokens all get 401 here. (A different code, 403, means "I know you, but no".) It is like a door that stays shut because you showed no ID.
    In this project: `http.Error(w, "unauthorized", http.StatusUnauthorized)` in [internal/auth/auth.go](../../../internal/auth/auth.go)

15. **Middleware gate** — The verifier is middleware (a wrapper around handlers), so one check protects many routes. Routes inside the group need a valid token. `/health` and the catalog routes are outside it. It is like one security gate in front of a whole wing of the building.
    In this project: `authed.Use(verifier.Middleware)` in [internal/httpapi/router.go](../../../internal/httpapi/router.go)

16. **Request context** — A Go `context.Context` travels with a request. The middleware stores the verified claims in it, and handlers read them back with `auth.FromContext`. It is like pinning a visitor badge on the visitor for every room to see.
    In this project: `FromContext` in [internal/auth/auth.go](../../../internal/auth/auth.go)

17. **whoami endpoint** — `GET /api/v1/whoami` returns the caller's `sub` and `email` from the claims. It exists to prove the whole chain works. It is like a mirror at the door that shows who the guard thinks you are.
    In this project: `handleWhoami` in [internal/httpapi/router.go](../../../internal/httpapi/router.go)

18. **SRP vs USER_PASSWORD_AUTH** — SRP is a login protocol where the password is never sent. cognito-local does not support it, so the app client enables `ALLOW_USER_PASSWORD_AUTH` and the web code asks for that flow. It is like using a simpler lock on a practice door.
    In this project: [scripts/seed-cognito-local.sh](../../../scripts/seed-cognito-local.sh)

19. **Idempotent script** — An idempotent script gives the same result if you run it twice. The seed script reuses the pool and client if they exist. It is like a light switch labelled "ON": pressing it again changes nothing.
    In this project: `EXISTING_POOL_ID` logic in [scripts/seed-cognito-local.sh](../../../scripts/seed-cognito-local.sh)

20. **Test double (self-signed JWKS server)** — A test double replaces a real dependency in a test. The auth tests start a tiny local HTTP server that serves their own public key, so tests need no Docker. It is like practising a driving test in an empty car park.
    In this project: `newTestIssuer` in [internal/auth/auth_test.go](../../../internal/auth/auth_test.go)

21. **Unit test** — A unit test checks one small piece of code, fast, with no outside services. All five auth tests are unit tests (no build tag). It is like testing one lightbulb before wiring the house.
    In this project: `TestMiddleware_ValidToken` and four siblings in [internal/auth/auth_test.go](../../../internal/auth/auth_test.go)

22. **Browse without login** — The API gates only identity-bearing routes. Seeing events and the seat map needs no account. It is like a museum with free halls and a paid special exhibit.
    In this project: route groups in [internal/httpapi/router.go](../../../internal/httpapi/router.go)

Background on why Cognito/JWT sits at the edge: see "Local stack" and "Terraform modules" in [plan.md](../../plan.md). The deep-dive has no dedicated auth section, so I link none.
