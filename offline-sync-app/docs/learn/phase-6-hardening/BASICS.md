# Phase 6 — Hardening: Basics

Siblings: [FILES.md](FILES.md) · [QUESTIONS.md](QUESTIONS.md) · [../README.md](../README.md)

Phase 6 makes the setup safer and measures it. It narrows permissions, adds a web firewall, sets CORS rules and runs a local load test.

1. **Hardening** — Making a system harder to break or abuse, after it already works. You remove extra permissions and add guards. Think of fitting locks on doors of a house that is already built.
   In this project: [MILESTONES.md](../../../MILESTONES.md) (Phase 6)

2. **IAM** — AWS's permission system. An IAM role is a set of allowed actions that a program (here a Lambda) wears while it runs. Think of a work badge that opens some doors only.
   In this project: [modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf) (`aws_iam_role.lambda`)

3. **Least-privilege IAM** — Give a program only the permissions it needs, on only the things it needs. If it is hacked, the damage stays small. Think of a cleaner with a key to the office, not to the safe.
   In this project: [modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf) (`aws_iam_role_policy.lambda_app`)

4. **Wildcard resource** — A `*` in a policy that means "everything". Policies here name exact resources instead. The photos rule ends in `/*` but that means "any file inside this one bucket", not "any bucket". Think of "any room on floor 3" versus "any room in the world".
   In this project: [modules/compute/main.tf](../../../infra/terraform/modules/compute/main.tf) (`Resource = "${var.photos_bucket_arn}/*"`)

5. **WAF (web application firewall)** — A filter that looks at each web request and blocks ones that look like attacks. It sits in front of your site. Think of a bag check at a stadium gate.
   In this project: [modules/waf/main.tf](../../../infra/terraform/modules/waf/main.tf)

6. **Managed rule group** — A ready-made set of WAF rules that AWS writes and updates for you. This project uses three: `AWSManagedRulesCommonRuleSet`, `AWSManagedRulesKnownBadInputsRuleSet`, `AWSManagedRulesSQLiRuleSet`. Think of buying an alarm system with the rules already set.
   In this project: [modules/waf/main.tf](../../../infra/terraform/modules/waf/main.tf)

7. **SQL injection** — An attack where the attacker types database commands into a form field. A WAF rule group can spot common ones. Good code also keeps data and commands apart (sqlc uses parameters). Think of a note on a form saying "also open the vault".
   In this project: [modules/waf/main.tf](../../../infra/terraform/modules/waf/main.tf) (`AWSManagedRulesSQLiRuleSet`)

8. **CloudFront** — AWS's content delivery network: servers around the world in front of your site. It can also carry a WAF. Think of a chain of reception desks in front of one office.
   In this project: [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf) (`aws_cloudfront_distribution.api`)

9. **Why CloudFront in front of the API** — The API type used here (API Gateway HTTP API) cannot attach a WAF directly. CloudFront can. So the project puts CloudFront in front of the API only to get a place to attach the WAF. Think of putting a door in front of a wall so you can fit a lock.
   In this project: [modules/waf/main.tf](../../../infra/terraform/modules/waf/main.tf) (top comment)

10. **Caching disabled** — The API gives different answers to each user, so CloudFront must never keep and re-serve a response. The project uses AWS's managed "CachingDisabled" policy and forwards the request headers. Think of a waiter who never reuses yesterday's order.
    In this project: [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf) (`cache_policy_id`, `origin_request_policy_id`)

11. **Provider alias (us-east-1)** — A WAF used with CloudFront must be created in the AWS region us-east-1, whatever region the rest of the stack uses. Terraform handles that with a second, named provider config. Think of a form that can only be filed at head office.
    In this project: [envs/local/main.tf](../../../infra/terraform/envs/local/main.tf) (`alias = "us_east_1"`)

12. **Origin and CORS** — An origin is the site address a page was loaded from (scheme, host, port). Browsers stop a page from one origin reading answers from another unless the server says "allowed". That permission is CORS. Think of a club that only admits guests on the list.
    In this project: [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf) (`cors_configuration`)

13. **CORS preflight** — Before some requests, the browser first sends a small `OPTIONS` question: "may I?". If the answer is yes, it sends the real request. A request with an `Authorization` header triggers it. Think of calling ahead to check the shop is open. With API Gateway's own CORS setting, the gateway answers this question without starting the Lambda (as MILESTONES describes).
    In this project: [modules/api/main.tf](../../../infra/terraform/modules/api/main.tf) (`max_age = 300` lets the browser remember the answer)

14. **Local CORS vs prod CORS** — Locally there is no API Gateway, so the chi `cors` middleware answers, and it allows `http://localhost:5173`. In prod the API Gateway setting allows the web CloudFront address. Think of two door lists, one per building.
    In this project: [router.go](../../../internal/httpapi/router.go) (`cors.Handler`)

15. **Secrets Manager wiring** — The DB password lives in Secrets Manager, and RDS Proxy reads it with its own role. Phase 5 set this up; Phase 6 just checks the permissions are narrow. Think of a safe whose key only one guard holds.
    In this project: [modules/database/main.tf](../../../infra/terraform/modules/database/main.tf) (`aws_iam_role_policy.rds_proxy_secrets`)

16. **Load test** — A test that sends many requests at once to see how fast the server stays. It finds slow spots before users do. Think of a rush-hour dress rehearsal for a bridge.
    In this project: [scripts/loadtest/main.go](../../../scripts/loadtest/main.go)

17. **Throughput** — How many requests are finished per second (req/s). Think of how many cars cross a bridge per minute. The Phase 6 run saw it stop growing at about 4,800 req/s on one laptop-class setup. That is not a production number.
    In this project: [MILESTONES.md](../../../MILESTONES.md) (Phase 6)

18. **Latency, p50, p95, p99** — Latency is how long one request takes. p50 is the middle: half of requests are faster. p95 means 95 out of 100 are faster; it shows the slow tail. Think of a race: p50 is the median runner, p95 is a slow-ish runner. The tool prints p50, p95, p99 and max.
    In this project: [scripts/loadtest/main.go](../../../scripts/loadtest/main.go) (`pct`)

19. **Concurrency** — How many requests are in flight at the same time. The tool flag `-c` sets it. Think of how many customers stand at the counter at once. Phase 6 tried 10, 50, 100 and 200.
    In this project: [scripts/loadtest/main.go](../../../scripts/loadtest/main.go) (`sem := make(chan struct{}, *concurrency)`)

20. **Connection pool and queuing** — A pool is a small fixed set of open database connections that requests share. If more requests arrive than connections, the rest wait in line. That waiting makes latency grow while throughput stays flat. Think of 4 tills and a growing queue. This is MILESTONES's reading of the result, not something proved by a separate experiment.
    In this project: [cmd/server/main.go](../../../cmd/server/main.go) (`pgxpool.New`)

21. **Plateau** — The point where adding more load no longer adds throughput. The line goes flat. Think of a motorway that is full: more cars do not mean more arrivals.
    In this project: [MILESTONES.md](../../../MILESTONES.md) (Phase 6)

22. **Caveat on benchmarks** — A load test on one laptop with one database and one Go process does not predict AWS. Real Lambda has cold starts, RDS Proxy and Aurora Serverless scaling. Use the numbers to spot regressions, not to size a launch. Think of timing a sprint on a treadmill.
    In this project: [MILESTONES.md](../../../MILESTONES.md) (Phase 6, "Caveat documented")
