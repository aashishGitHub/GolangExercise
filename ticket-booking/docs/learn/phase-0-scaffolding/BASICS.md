# Phase 0 — Scaffolding: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [learn index](../README.md)

Phase 0 builds the empty house: a Go program that answers `GET /health`, a local stack of fake cloud services, build commands, and a CI check. No ticket logic yet.

1. **Go module** — A Go module is one folder tree that Go builds and versions as a single unit. Its name and Go version live in one file called `go.mod`. Think of it as the label on a moving box that says what is inside and which tools you need to unpack it.
   In this project: [go.mod](../../../go.mod)

2. **Monorepo** — A monorepo is one Git repository that holds several projects. Here `ticket-booking/` sits next to `offline-sync-app/` and other exercises. It is like one apartment building with many flats and one front door.
   In this project: [ticket-booking/](../../../) (the CI file at the repo root filters by this folder)

3. **Environment variable** — An environment variable is a named setting the operating system hands to a program when it starts, such as `ADDR=:8080`. The program reads it instead of having the value written in code. It is like a sticky note on the oven door: same oven, different temperature each time.
   In this project: [.env.example](../../../.env.example)

4. **Config struct** — A struct is a Go type that groups named fields. `Config` groups every setting the server needs, and one function, `Load()`, fills it from environment variables. It is like one form that every part of the app reads from, instead of each part asking the OS on its own.
   In this project: [internal/config/config.go](../../../internal/config/config.go)

5. **Default value (fallback)** — A default is the value used when the environment variable is not set. `getenv("ADDR", ":8080")` means "use ADDR, else `:8080`". It is like a thermostat that returns to 20 degrees when nobody touched it.
   In this project: `getenv` in [internal/config/config.go](../../../internal/config/config.go)

6. **Local-only setting defaults to empty** — Some settings point at a fake service on your laptop, for example a fake S3 address. The package comment says these should default to empty, so a production server never talks to a dev fake by accident. It is like a spare key you must pick up on purpose, not one left in the lock.
   In this project: `S3Endpoint` in [internal/config/config.go](../../../internal/config/config.go)

7. **HTTP server** — An HTTP server is a program that listens on a network port and answers requests with responses. Go's `net/http` package does this. It is like a shop counter: customers (browsers) ask, the clerk answers.
   In this project: [cmd/server/main.go](../../../cmd/server/main.go)

8. **Router** — A router looks at the request method and path (`GET /health`) and picks the function that handles it. This project uses a library called chi. It is like a mail room sorting letters into pigeonholes by address.
   In this project: `NewRouter` in [internal/httpapi/router.go](../../../internal/httpapi/router.go)

9. **Middleware** — Middleware is a wrapper function that runs before (and sometimes after) your handler for every request. Examples here: attach a request ID, recover from a crash, add CORS headers. It is like airport security: every passenger passes it before reaching any gate.
   In this project: `r.Use(...)` calls in [internal/httpapi/router.go](../../../internal/httpapi/router.go)

10. **Health check** — A health check is a cheap endpoint that says "I am alive". Load balancers and humans call it. Here `GET /health` returns `{"status":"ok"}`. It is like asking "are you awake?" and hearing "yes".
    In this project: `handleHealth` in [internal/httpapi/router.go](../../../internal/httpapi/router.go)

11. **CORS (cross-origin resource sharing)** — Browsers block a web page from calling a different origin (host + port) unless that server says it is allowed. `curl` does not enforce this, a browser does. It is like a club bouncer who checks a guest list that only the browser respects.
    In this project: `cors.Handler` in [internal/httpapi/router.go](../../../internal/httpapi/router.go)
    > Note: MILESTONES lists CORS as a Phase 4 bug fix, so it was not in the Phase 0 code. It is in the file now.

12. **Docker container** — A container is a packaged program plus everything it needs, running isolated from the rest of your machine. You start one from an image (the frozen package). It is like a lunchbox: the meal and cutlery travel together.
    In this project: [docker-compose.yml](../../../docker-compose.yml)

13. **Docker Compose** — Compose starts several containers together from one YAML file. `docker compose up -d` starts them in the background. It is like a stage manager calling every actor on stage at once.
    In this project: `compose-up` and `compose-down` in [Makefile](../../../Makefile)

14. **Emulator (local fake)** — An emulator imitates a cloud service on your laptop so you need no cloud account. This project fakes Cognito (login), S3 (files), and EventBridge/SQS (messages) with cognito-local, MinIO, and moto. It is like a flight simulator: same controls, no real sky.
    In this project: services `cognito-local`, `minio`, `moto-server` in [docker-compose.yml](../../../docker-compose.yml)

15. **Image pinning** — Pinning means naming an exact version of a container image, like `postgres:16` or `cognito-local:4.0.0`, instead of `latest`. Then a new upstream release cannot silently break you. It is like writing the exact recipe edition instead of "the newest cookbook".
    In this project: `image:` lines in [docker-compose.yml](../../../docker-compose.yml)

16. **Container healthcheck** — A healthcheck is a small command Docker runs again and again to see whether the service inside is ready, for example `pg_isready`. Another container can wait for it. It is like tapping the oven to check it reached temperature.
    In this project: `healthcheck:` under `postgres` and `redis` in [docker-compose.yml](../../../docker-compose.yml)

17. **Postgres and Redis (roles)** — Postgres is a database that stores data safely on disk. Redis is a very fast in-memory store. In this project Postgres is the final judge of seat state and Redis is only a fast helper. It is like a court record (Postgres) versus a sticky-note board (Redis).
    In this project: services `postgres` and `redis` in [docker-compose.yml](../../../docker-compose.yml)

18. **Makefile** — A Makefile gives short names to long commands, so `make test` runs `go test ./...`. It is like speed-dial on a phone.
    In this project: [Makefile](../../../Makefile)

19. **.env and .gitignore** — A `.env` file holds local settings. `.env.example` is a safe template committed to Git, while the real `.env` and `.env.local` are ignored by Git. It is like handing out a blank form, not your filled-in one.
    In this project: [.gitignore](../../../.gitignore) and [.env.example](../../../.env.example)

20. **CI (continuous integration)** — CI is a robot that builds and tests your code on every push. Here GitHub Actions runs the Go, web, and Terraform checks. It is like a proofreader who reads every page before it is printed.
    In this project: [.github/workflows/ticketing-ci.yml](../../../../.github/workflows/ticketing-ci.yml)

21. **Terraform** — Terraform describes cloud infrastructure in text files, and a tool turns that text into real servers. It is like an architect's blueprint that a builder follows.
    In this project: [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf)

22. **validate vs apply** — `terraform validate` only checks that the files are well-formed. `terraform apply` creates real, billable AWS resources. This project runs validate automatically and leaves apply as a manual, explicit step. It is like proofreading a cheque versus signing it.
    In this project: `tf-validate` in [Makefile](../../../Makefile)

23. **Provider alias** — A provider is the plugin Terraform uses to talk to a cloud. An alias is a second copy of it for another region. Here an extra `us_east_1` alias exists because the file says CloudFront-scope firewall (WAF) resources must live in that region. It is like keeping a second phone line for another country.
    In this project: `provider "aws"` blocks in [infra/terraform/envs/local/main.tf](../../../infra/terraform/envs/local/main.tf)

24. **Cross-compile** — Cross-compiling builds a program for a different operating system or CPU than your own. `make build-lambda` builds Linux arm64 binaries on your Mac. It is like writing a letter in the recipient's language.
    In this project: `build-lambda` in [Makefile](../../../Makefile)

Background theory for the whole design: [deep-dive.md](../../deep-dive.md).
