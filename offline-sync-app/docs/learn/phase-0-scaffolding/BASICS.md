# Phase 0 — Scaffolding: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

Phase 0 builds the empty house: a Go server that answers `/health`, a React app shell, local services in Docker, a Makefile, and an empty Terraform stack. No features yet.

1. **Go module** — A module is a folder of Go code with a `go.mod` file at its top. The file holds the module name and the libraries it needs. Think of it as the title page and shopping list of a book. In this project: [go.mod](../../../go.mod) (module name `fieldsync`).

2. **Package** — A package is one folder of Go files that share a name. Other code imports it by path, like `fieldsync/internal/auth`. Think of a drawer in a toolbox: each drawer holds one kind of tool. In this project: [internal/httpapi/](../../../internal/httpapi/router.go).

3. **cmd/ and internal/** — `cmd/` holds programs you can start (each has a `main` function). `internal/` holds shared code that Go only lets this module import. Think of `cmd/` as the front door and `internal/` as the rooms behind it. In this project: [cmd/server/main.go](../../../cmd/server/main.go).

4. **Entrypoint** — The entrypoint is the `main` function where a program starts. It reads settings, builds the parts, and starts them. Think of it as the person who turns the key and switches on the lights. In this project: [cmd/server/main.go](../../../cmd/server/main.go).

5. **Router** — A router looks at each web request (method plus path) and picks which function answers it. `GET /health` goes to one function, `POST /api/sync` to another. Think of a hotel desk sending guests to the right room. In this project: [internal/httpapi/router.go](../../../internal/httpapi/router.go) using the `chi` library.

6. **Handler** — A handler is the function that answers one kind of request. It reads the request and writes the reply. Think of the cook who makes one dish. In this project: `healthHandler` in [internal/httpapi/health.go](../../../internal/httpapi/health.go).

7. **Middleware** — Middleware is code that runs before (or around) every handler. It can log, add an ID, catch crashes, or check permission. Think of airport security that every passenger passes before the gate. In this project: `r.Use(middleware.RequestID)`, `Logger`, `Recoverer`, and CORS in [router.go](../../../internal/httpapi/router.go).

8. **Health check** — A health check is a tiny URL that says "I am alive". Tools call it to see if the server runs. Think of tapping someone's shoulder to see if they are awake. In this project: `GET /health` returns `{"status":"ok"}` ([health.go](../../../internal/httpapi/health.go)).

9. **CORS** — A browser blocks a web page from calling a different origin (host plus port) unless that server says it is allowed. CORS is how the server says "yes". Think of a club bouncer with a guest list. In this project: `cors.Handler` allows `http://localhost:5173`, the Vite dev server ([router.go](../../../internal/httpapi/router.go)).

10. **Environment variable** — A setting passed to a program from outside, like `ADDR=:8080`. The code stays the same; the setting changes per machine. Think of a thermostat dial you turn without rebuilding the house. In this project: [internal/config/config.go](../../../internal/config/config.go) (`getenv`).

11. **Config with fallback** — `getenv(key, fallback)` returns the variable if it is set and not empty, otherwise a default. Local dev works with no setup; prod sets real values. Think of a recipe that says "use butter, or oil if none". In this project: [config.go](../../../internal/config/config.go).

12. **Docker Compose** — Compose starts several containers (small isolated programs) from one file. You type one command and get a database, a login service, and storage. Think of a stage crew setting up all props at once. In this project: [docker-compose.yml](../../../docker-compose.yml).

13. **Emulator** — An emulator is a free local program that pretends to be a paid cloud service. It speaks the same protocol, so the code does not know the difference. Think of a flight simulator. In this project: `cognito-local` (login), `minio` (S3 storage), `moto-server` (EventBridge) in [docker-compose.yml](../../../docker-compose.yml).

14. **Postgres** — Postgres is a database that stores data in tables and answers SQL questions. This project runs the real one (`postgres:16`), not an emulator. Think of a very strict filing cabinet. In this project: the `postgres` service in [docker-compose.yml](../../../docker-compose.yml).

15. **Migration** — A migration is a numbered SQL file that changes the database shape (add a table, add a column). Files run in order, so every machine ends up with the same tables. Think of numbered steps in a building plan. In this project: [migrations/](../../../migrations/000001_init_schema.up.sql), applied by `make migrate`.

16. **sqlc** — sqlc reads your SQL queries and writes the matching Go code for you. You write SQL; it writes the typed functions. Think of a translator who prints a phrasebook. In this project: [sqlc/sqlc.yaml](../../../sqlc/sqlc.yaml), output in `internal/db`, run by `make sqlc-generate`.

17. **Makefile** — A Makefile gives short names to long commands. `make test` instead of remembering flags. Think of speed-dial on a phone. In this project: [Makefile](../../../Makefile).

18. **Vite** — Vite is the tool that serves the React app while you code and bundles it for release. It reloads the page when you save. Think of a workshop that rebuilds the model each time you tweak it. In this project: [web/vite.config.ts](../../../web/vite.config.ts).

19. **PWA (progressive web app)** — A PWA is a website that can be installed and can work with no network. It needs a manifest (name, colors) and a service worker. Think of a website with a suitcase packed for the trip. In this project: `VitePWA({...})` in [vite.config.ts](../../../web/vite.config.ts).

20. **Service worker** — A service worker is a script the browser runs in the background. It can answer requests from a saved copy when you are offline. Think of a butler who keeps spare copies of the mail. In this project: generated by `vite-plugin-pwa`; `/api` paths are kept out of its navigation fallback ([vite.config.ts](../../../web/vite.config.ts)).

21. **Terraform** — Terraform describes cloud things (databases, functions) in files, then builds them. `validate` checks the files are well formed without touching the cloud. Think of an architect's blueprint, not the building. In this project: [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf); `make tf-validate` never applies.

22. **.gitignore** — A list of files git must not save: secrets, build output, downloaded libraries. Think of items you leave out of the moving box. In this project: [.gitignore](../../../.gitignore) (`.env`, `.env.local`, `web/node_modules/`).

23. **"chi-lith"** — A monolith is one program that holds all the routes. This project keeps one chi router for the whole API on purpose, and runs the same router locally and on Lambda. Think of one restaurant kitchen instead of ten food stalls. In this project: [router.go](../../../internal/httpapi/router.go) used by [cmd/server/main.go](../../../cmd/server/main.go) and [cmd/lambda/main.go](../../../cmd/lambda/main.go).
