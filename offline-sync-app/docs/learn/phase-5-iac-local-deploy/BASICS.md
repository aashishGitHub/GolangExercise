# Phase 5 — IaC and Local Deploy Loop: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

Phase 5 describes the whole AWS setup as code and proves the code is well formed. It never creates anything in AWS. It also makes the same Go router run as one AWS Lambda.

1. **Infrastructure as code (IaC)** — You write the servers, networks and buckets you want as text files, not by clicking in a web console. A tool reads the files and builds them. Think of a recipe card versus cooking from memory each time.
   In this project: [envs/local/main.tf](../../../infra/terraform/envs/local/main.tf)

2. **Terraform** — The IaC tool used here. You describe what should exist, and Terraform works out what to create, change or delete. Think of giving a builder a blueprint instead of a to-do list.
   In this project: [infra/terraform](../../../infra/terraform/)

3. **Terraform module** — A folder of Terraform files that builds one logical piece, like "the network". Other folders pass it inputs (variables) and read its results (outputs). Think of a LEGO kit with a box label.
   In this project: [modules/network/main.tf](../../../infra/terraform/modules/network/main.tf)

4. **Environment folder (`envs/local`)** — The one place that picks the modules, wires them together and sets the provider. Modules do not call each other directly. The env passes `module.network.vpc_id` into `module.database`. Think of a head chef assembling dishes from stations.
   In this project: [envs/local/main.tf](../../../infra/terraform/envs/local/main.tf)

5. **Provider** — A plug-in that lets Terraform talk to one service, here AWS. The config here uses fake keys and skips AWS checks so `init` and `validate` work with no account. Think of a phone charger that fits the wall but is not plugged in.
   In this project: [envs/local/main.tf](../../../infra/terraform/envs/local/main.tf) (`provider "aws"`)

6. **State** — A file where Terraform remembers what it built, so the next run only changes the difference. State can hold secret values in plain text, so treat it as secret. Think of a stock list in a shop. `*.tfstate` is git-ignored here.
   In this project: [.gitignore](../../../.gitignore)

7. **init, validate, plan, apply** — `init` downloads providers. `validate` checks the files are well formed. `plan` shows what would change. `apply` really builds it. This phase runs only `init` and `validate`. Think of spell-check versus printing and mailing the letter.
   In this project: [Makefile](../../../Makefile) (`tf-validate`)

8. **VPC and subnets** — A VPC is your private network inside AWS. Subnets are sections of it. Public subnets can reach the internet; private ones hide the database and Lambda. Think of a building with a lobby (public) and locked offices (private).
   In this project: [modules/network/main.tf](../../../infra/terraform/modules/network/main.tf)

9. **NAT gateway** — A one-way door that lets private things call out to the internet without letting the internet call in. This project uses one NAT for both zones to save cost. Think of a receptionist who makes outgoing calls for staff.
   In this project: [modules/network/main.tf](../../../infra/terraform/modules/network/main.tf) (`aws_nat_gateway.main`)

10. **Security group** — A firewall rule list on a resource: who may connect to it, and on which port. Here only the Lambda group may reach the proxy, and only the proxy may reach the database. Think of a guest list at each door.
    In this project: [modules/network/main.tf](../../../infra/terraform/modules/network/main.tf)

11. **Aurora Serverless v2** — A managed Postgres database on AWS that grows and shrinks its own size between a minimum and maximum. Think of a restaurant that adds tables when it is busy.
    In this project: [modules/database/main.tf](../../../infra/terraform/modules/database/main.tf)

12. **Connection pool and RDS Proxy** — A pool keeps a few open database connections and shares them. RDS Proxy is AWS's pool in front of the database. Each Lambda copy would otherwise open its own connection and use up the limit. Think of a taxi rank sharing a few cars among many riders.
    In this project: [modules/database/main.tf](../../../infra/terraform/modules/database/main.tf) (`aws_db_proxy.main`)

13. **Secrets Manager** — An AWS vault for passwords. Terraform makes a random password and stores it there. Think of a hotel safe. Be careful: the generated value still sits in Terraform state (see the L4 tier in QUESTIONS.md).
    In this project: [modules/secrets/main.tf](../../../infra/terraform/modules/secrets/main.tf)

14. **Cognito user pool** — AWS's sign-up and sign-in service. It hands out signed tokens that prove who a user is. Think of a club doorman who stamps your hand.
    In this project: [modules/auth/main.tf](../../../infra/terraform/modules/auth/main.tf)

15. **S3 and CloudFront** — S3 stores files in buckets. CloudFront is a worldwide cache and front door in front of them. Both buckets here are fully private and only CloudFront may read them. Think of a warehouse (S3) with a shop window (CloudFront).
    In this project: [modules/storage/main.tf](../../../infra/terraform/modules/storage/main.tf)

16. **Origin Access Control (OAC)** — A signed "I am CloudFront" pass. The bucket policy lets only that one distribution read objects. Think of a key that opens only one door.
    In this project: [modules/storage/main.tf](../../../infra/terraform/modules/storage/main.tf) (`aws_cloudfront_origin_access_control`)

17. **SPA fallback** — A single-page app has one real file, `index.html`. If someone opens `/anything`, the host must serve `index.html`, and the app draws the right screen. The web CloudFront turns 403 and 404 into `index.html` with status 200. Think of a hotel desk that sends every guest to the same lobby.
    In this project: [modules/storage/main.tf](../../../infra/terraform/modules/storage/main.tf) (`custom_error_response`)

18. **Lambda** — A function AWS runs for you only when a request comes in. You do not manage a server. Think of a taxi, not a car you own. Here one Lambda runs the whole API.
    In this project: [modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf)

19. **Cross-compile and `bootstrap`** — Go can build a Linux ARM program from a Mac. A Lambda on `provided.al2023` runs a file named `bootstrap`, zipped. Terraform reads that zip, so you build it first. Think of baking the cake before the shop opens.
    In this project: [Makefile](../../../Makefile) (`build-lambda`)

20. **Lambda adapter** — A small piece of code that turns an API Gateway event into a normal HTTP request, and back. So the same router runs locally and on Lambda. Think of a travel plug adapter.
    In this project: [cmd/lambda/main.go](../../../cmd/lambda/main.go)

21. **API Gateway HTTP API and JWT authorizer** — API Gateway is the public front door for your API. A JWT authorizer checks the token before your Lambda even starts. Think of airport security before the gate. `/health` stays open.
    In this project: [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf)

22. **Environment variable config** — The program reads its settings from the environment, not from hard-coded text. An empty value means "use the real AWS default". Think of a TV that uses factory settings until you change them.
    In this project: [internal/config/config.go](../../../internal/config/config.go)

23. **CI (continuous integration)** — A robot that runs build and tests on every push. Here it builds and tests Go, builds the web app, and runs `terraform validate`. It deploys nothing. Think of a spell-checker that runs on every save.
    In this project: [.github/workflows/fieldsync-ci.yml](../../../../.github/workflows/fieldsync-ci.yml)

24. **Path filter** — A CI rule that runs the job only if certain folders changed. This repo holds many projects, so the filter is `offline-sync-app/**`. Think of a doorbell that rings only for your flat.
    In this project: [.github/workflows/fieldsync-ci.yml](../../../../.github/workflows/fieldsync-ci.yml) (`on.push.paths`)
