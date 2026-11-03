# Phase 1 — Auth: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

Phase 1 answers one question: how does the server know who is calling? The browser logs in with Cognito and gets a token. The Go server checks that token on every `/api` call.

1. **Authentication** — Authentication means proving who you are. Authorization means what you are allowed to do. This phase does the first one (who is it?). Think of showing ID at the door versus which rooms your badge opens. In this project: [internal/auth/auth.go](../../../internal/auth/auth.go).

2. **Amazon Cognito** — Cognito is the AWS service that stores users and checks passwords. Your app does not keep passwords. Think of a bank vault: the app asks the vault, never holds the cash. In this project: real Cognito in prod ([infra/terraform/modules/auth/main.tf](../../../infra/terraform/modules/auth/main.tf)), `cognito-local` on your laptop.

3. **User pool and app client** — A user pool is the list of users. An app client is a registered app allowed to talk to that pool; it has a client ID. Think of a club (pool) and the door the web app uses (client). In this project: made by [scripts/seed-cognito-local.sh](../../../scripts/seed-cognito-local.sh) (`fieldsync-local` pool, `fieldsync-web` client).

4. **Token** — After login, Cognito hands the browser a small signed string. The browser shows it on later calls instead of a password. Think of a wristband at a festival. In this project: Amplify holds it; [AuthPanel.tsx](../../../web/src/auth/AuthPanel.tsx) reads it with `fetchAuthSession`.

5. **JWT (JSON Web Token)** — A JWT is a token with three dot-separated parts: header, claims (the data), and signature. Anyone can read the claims. Only the issuer can make a valid signature. Think of a letter with a wax seal. In this project: parsed in `VerifyToken` in [auth.go](../../../internal/auth/auth.go).

6. **Claim** — A claim is one fact inside the token, like `sub` (user ID), `email`, `iss` (who issued it), `aud` (who it is for), `exp` (when it expires). Think of the fields printed on an ID card. In this project: `Claims{Sub, Email}` in [auth.go](../../../internal/auth/auth.go).

7. **`sub`** — `sub` (subject) is the user's permanent unique ID in the pool. Emails can change; `sub` does not. Think of a social security number versus a nickname. In this project: later phases save it as `created_by` ([Phase 2](../phase-2-sync-api-db/BASICS.md)).

8. **ID token vs access token** — Cognito issues both. The ID token describes the user (email, and so on). The access token is for calling APIs. This server only accepts `token_use` = `id`. Think of a name badge versus a door key. In this project: the `token_use` check in `VerifyToken`; test `TestMiddleware_WrongTokenUse`.

9. **Signature and RS256** — RS256 uses a key pair. The issuer signs with a private key nobody else has. Anyone can check with the public key. Think of a stamp only the post office owns, which anyone can recognize. In this project: signed in tests by `jwt.Sign(... jwa.RS256() ...)` in [auth_test.go](../../../internal/auth/auth_test.go).

10. **JWKS** — A JWKS (JSON Web Key Set) is a public web page listing the issuer's public keys. The server downloads it to check signatures. Think of a public phone book of stamp designs. In this project: `{issuer}/.well-known/jwks.json`, fetched by `jwk.Fetch` in `keySet` ([auth.go](../../../internal/auth/auth.go)).

11. **Issuer and audience** — The issuer is who made the token. The audience is which app the token is for. The server rejects a token from the wrong issuer or for the wrong app. Think of checking that a ticket is for this concert, sold by this box office. In this project: `jwt.WithIssuer`, `jwt.WithAudience` in [auth.go](../../../internal/auth/auth.go); test `TestMiddleware_WrongAudience`.

12. **Expiry** — A token stops working after its `exp` time. A stolen token is only useful for a short while. Think of milk with a date on it. In this project: test `TestMiddleware_ExpiredToken` signs a token that expired an hour ago and expects 401.

13. **Bearer header** — The browser sends the token in the HTTP header `Authorization: Bearer <token>`. "Bearer" means whoever holds it gets in. Think of cash: whoever holds it can spend it. In this project: `strings.CutPrefix(authHeader, "Bearer ")` in `verify` ([auth.go](../../../internal/auth/auth.go)).

14. **401 Unauthorized** — The HTTP status code that means "I do not know who you are". The middleware sends it for a missing, expired, or invalid token. Think of a guard saying "no ID, no entry". In this project: `http.Error(w, "unauthorized", http.StatusUnauthorized)` in `Middleware`.

15. **TTL cache** — Fetching the key list on every request would be slow. The server keeps it in memory for a time-to-live (TTL), here 5 minutes, then fetches again. A mutex (a lock) lets only one goroutine touch the cache at a time. Think of copying a phone book page and re-copying it every 5 minutes. In this project: `Verifier.ttl`, `mu`, `fetchedAt` in [auth.go](../../../internal/auth/auth.go).

16. **Go context value** — A Go `context.Context` travels with a request. You can store a value in it, here the caller's identity, and handlers further down can read it. Think of a name tag clipped to a parcel as it moves along the belt. In this project: `context.WithValue(...)` in `Middleware`, `FromContext` in [auth.go](../../../internal/auth/auth.go).

17. **Amplify Auth** — Amplify is an AWS JavaScript library with ready functions: `signUp`, `confirmSignUp`, `signIn`, `signOut`. It stores the session for you. Think of a remote control for Cognito. In this project: [web/src/amplifyConfig.ts](../../../web/src/amplifyConfig.ts) and [AuthPanel.tsx](../../../web/src/auth/AuthPanel.tsx).

18. **SRP vs USER_PASSWORD_AUTH** — SRP is a login method that never sends the password over the wire. Real Cognito supports it. `cognito-local` does not, so local sign-in sends the password with `USER_PASSWORD_AUTH` instead. Think of a secret handshake versus saying the password out loud. In this project: `authFlowType: 'USER_PASSWORD_AUTH'` in [AuthPanel.tsx](../../../web/src/auth/AuthPanel.tsx).

19. **Confirmation code** — After sign-up, Cognito asks for a code sent by email. Locally the code is fixed to `123456`. Think of a fake "reply code" that always works in a demo. In this project: `CODE: "123456"` in [docker-compose.yml](../../../docker-compose.yml).

20. **Idempotent script** — A script that is safe to run twice. The seed script looks for the pool and client first and only creates them if missing. Think of a light switch set to "on": pressing "on" again changes nothing. In this project: [scripts/seed-cognito-local.sh](../../../scripts/seed-cognito-local.sh).

21. **CORS preflight** — Before some cross-origin calls, the browser sends an `OPTIONS` request first to ask permission. The server must answer it. `curl` never does this, so a call can work in `curl` and fail in the browser. Think of a phone call to ask "may I visit?" before you knock. In this project: `cors.Handler` in [router.go](../../../internal/httpapi/router.go) (`Authorization` is in `AllowedHeaders`).

22. **Auth gates sync, not capture** — A field worker with no signal must still record data. So login is required only to send data to the server, not to write it on the phone. Think of writing in a notebook anywhere, but needing a pass to enter the archive. In this project: `verifier.Middleware` wraps only the `/api` group in [router.go](../../../internal/httpapi/router.go).

23. **Unit test with a fake issuer** — The test starts a tiny local web server that serves a JWKS, makes a fresh key pair, and signs its own tokens. No Cognito needed. Think of a flight simulator for the login check. In this project: `newTestIssuer` and `signToken` in [auth_test.go](../../../internal/auth/auth_test.go).
