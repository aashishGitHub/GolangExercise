SELECT id, event_type, occurred_at
FROM domain_events
WHERE actor_sub = 'actor-07'
  AND occurred_at >= timestamptz '2026-09-14 00:00+00'
ORDER BY occurred_at;
