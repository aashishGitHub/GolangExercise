SELECT count(*) AS processed, max(processed_at) AS latest
FROM processed_events
WHERE consumer_name = 'notifier'
  AND processed_at >= timestamptz '2026-09-24 00:00+00';
