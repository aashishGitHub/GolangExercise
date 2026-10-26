SELECT id, event_type, occurred_at, published_at
FROM domain_events
WHERE aggregate_id = '6197a0fa-ea0a-a8b8-65bb-08e011a7210a'
ORDER BY occurred_at;
