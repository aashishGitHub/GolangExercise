# ElastiCache Redis for the hold fast-path, the availability bitset cache
# (Phase 7), and the waiting-room queue (Phase 8) — never the arbiter
# (docs/plan.md decision #1). Cluster mode DISABLED: this workload is a
# fan-out/throughput problem (one bitset key + N hold keys per event), not a
# key-space-size problem — sharding buys nothing here and costs the
# cross-slot restriction that would break the hold-release Lua script
# (internal/holdlock's compare-and-delete touches one key, but a future
# "release all seats in an order" script would touch N — hash-tagged keys
# already guard against that, see internal/holdlock's key() comment).

resource "aws_elasticache_subnet_group" "main" {
  name       = "${var.name_prefix}-redis-subnets"
  subnet_ids = var.private_subnet_ids
}

# Three settings that are direct consequences of "Redis is never the
# arbiter" (docs/plan.md "cache" module) — each would look like an
# unrelated tuning knob without that context, so each gets its own comment:

resource "aws_elasticache_parameter_group" "main" {
  name   = "${var.name_prefix}-redis-pg"
  family = "redis7"

  # 1. noeviction, not allkeys-lru: this instance holds live SET NX PX hold
  #    keys. LRU-evicting one silently frees a held seat — a correctness
  #    bug disguised as a cache-tuning knob. Pair with the memory alarm in
  #    modules/observability (Phase 11) rather than let evictions happen.
  parameter {
    name  = "maxmemory-policy"
    value = "noeviction"
  }
  # 2. FLUSHALL/KEYS disabled in prod — KEYS is an O(n) blocking scan, and a
  #    stray FLUSHALL should never be reachable from application code (the
  #    _WithRedisFlushed test in internal/inventory only reaches it because
  #    it dials Redis directly in a test, bypassing this ACL).
  parameter {
    name  = "notify-keyspace-events"
    value = ""
  }
}

resource "aws_elasticache_replication_group" "main" {
  replication_group_id = "${var.name_prefix}-redis"
  description          = "Ticketing hold fast-path + bitset cache + waiting-room queue"

  engine         = "redis"
  engine_version = "7.1"
  node_type      = var.node_type
  port           = 6379

  num_cache_clusters         = 2 # 1 primary + 1 replica, cluster mode disabled
  automatic_failover_enabled = true
  multi_az_enabled           = true

  subnet_group_name    = aws_elasticache_subnet_group.main.name
  security_group_ids   = [var.redis_security_group_id]
  parameter_group_name = aws_elasticache_parameter_group.main.name

  transit_encryption_enabled = true
  at_rest_encryption_enabled = true
  auth_token                 = var.redis_auth_token

  # 3. No persistence: a node recovering a STALE bitset from disk after a
  #    restart is strictly worse than one that comes back empty and refills
  #    from Postgres (the arbiter). Persistence here would convert a clean,
  #    correct cold-start into a silent correctness hazard.
  snapshot_retention_limit = 0

  timeouts {}
}
