//go:build integration

package analytics

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultDSN = "postgres://fieldsync:fieldsync@localhost:5432/fieldsync_analytics?sslmode=disable"

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	dsn := os.Getenv("ANALYTICS_DATABASE_URL")
	if dsn == "" {
		dsn = defaultDSN
	}
	var err error
	pool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		return 1
	}
	defer pool.Close()

	var name string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil {
		fmt.Fprintln(os.Stderr, "current_database:", err)
		return 1
	}
	if !strings.Contains(name, "analytics") {
		fmt.Fprintf(os.Stderr, "refusing to run: database %q is not an analytics database\n", name)
		return 1
	}
	if err := seed(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		return 1
	}
	return m.Run()
}

func seed(ctx context.Context) error { return execScript(ctx, "seed/seed.sql") }

func execScript(ctx context.Context, path string) error {
	script, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Conn().PgConn().Exec(ctx, string(script)).ReadAll(); err != nil {
		_, _ = conn.Exec(ctx, "ROLLBACK")
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

type rec map[string]any

type rowset struct {
	cols []string
	rows []rec
}

func query(ctx context.Context, q pgxQuerier, sql string, args ...any) (rowset, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return rowset{}, err
	}
	defer rows.Close()
	var rs rowset
	for _, fd := range rows.FieldDescriptions() {
		rs.cols = append(rs.cols, fd.Name)
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return rowset{}, err
		}
		r := rec{}
		for i, c := range rs.cols {
			r[c] = vals[i]
		}
		rs.rows = append(rs.rows, r)
	}
	return rs, rows.Err()
}

type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func mustQuery(t *testing.T, sql string, args ...any) rowset {
	t.Helper()
	rs, err := query(context.Background(), pool, sql, args...)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	return rs
}

func scalarF(t *testing.T, sql string, args ...any) float64 {
	t.Helper()
	rs := mustQuery(t, sql, args...)
	if len(rs.rows) != 1 || len(rs.cols) != 1 {
		t.Fatalf("scalar query returned %d rows x %d cols", len(rs.rows), len(rs.cols))
	}
	f, ok := toFloat(rs.rows[0][rs.cols[0]])
	if !ok {
		t.Fatalf("scalar query returned non-numeric %v", rs.rows[0][rs.cols[0]])
	}
	return f
}

func scalarI(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	return int64(math.Round(scalarF(t, sql, args...)))
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case pgtype.Numeric:
		f, err := x.Float64Value()
		if err != nil || !f.Valid {
			return 0, false
		}
		return f.Float64, true
	}
	return 0, false
}

func (r rec) f(t *testing.T, col string) float64 {
	t.Helper()
	f, ok := toFloat(r[col])
	if !ok {
		t.Fatalf("column %q = %v (%T) is not numeric", col, r[col], r[col])
	}
	return f
}

func (r rec) i(t *testing.T, col string) int64 {
	t.Helper()
	return int64(math.Round(r.f(t, col)))
}

func (r rec) s(t *testing.T, col string) string {
	t.Helper()
	s, ok := r[col].(string)
	if !ok {
		t.Fatalf("column %q = %v (%T) is not text", col, r[col], r[col])
	}
	return s
}

func (r rec) b(t *testing.T, col string) bool {
	t.Helper()
	b, ok := r[col].(bool)
	if !ok {
		t.Fatalf("column %q = %v (%T) is not bool", col, r[col], r[col])
	}
	return b
}

func (r rec) ts(t *testing.T, col string) time.Time {
	t.Helper()
	ts, ok := r[col].(time.Time)
	if !ok {
		t.Fatalf("column %q = %v (%T) is not a timestamp", col, r[col], r[col])
	}
	return ts.UTC()
}

func approx(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v (+/- %v)", what, got, want, tol)
	}
}

func dateKey(ts time.Time) string { return ts.UTC().Format("2006-01-02") }

func queryFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("queries/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

func queryName(path string) string { return strings.TrimSuffix(filepath.Base(path), ".sql") }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var invariants = map[string]func(*testing.T, rowset){
	"01_moving_avg_per_actor":             checkMovingAvg,
	"02_top3_per_group_ties":              checkTop3,
	"03_time_to_first_photo":              checkTimeToFirstPhoto,
	"04_latency_percentiles":              checkLatencyPercentiles,
	"05_sessionization_islands":           checkSessions,
	"06_funnel_location_assessment_photo": checkFunnel,
	"07_weekly_retention_cohort":          checkRetention,
	"08_running_total_share":              checkRunningTotal,
	"09_outbox_backlog_consumer_lag":      checkBacklog,
	"10_anomaly_days":                     checkAnomalyDays,
	"11_dedupe_latest":                    checkDedupe,
	"12_conditional_pivot_filter":         checkPivot,
	"13_gaps_silent_hours":                checkSilentHours,
	"14_activity_streaks":                 checkStreaks,
}

func TestEveryQueryFileHasInvariants(t *testing.T) {
	files := queryFiles(t)
	if len(files) < 12 {
		t.Fatalf("found %d query files, want at least 12", len(files))
	}
	seen := map[string]bool{}
	for _, f := range files {
		name := queryName(f)
		seen[name] = true
		if _, ok := invariants[name]; !ok {
			t.Errorf("%s has no registered invariants", name)
		}
	}
	for name := range invariants {
		if !seen[name] {
			t.Errorf("invariants registered for %s but the file is missing", name)
		}
	}
}

func TestQueryInvariants(t *testing.T) {
	for _, f := range queryFiles(t) {
		name := queryName(f)
		check, ok := invariants[name]
		if !ok {
			continue
		}
		t.Run(name, func(t *testing.T) {
			rs := mustQuery(t, readFile(t, f))
			if len(rs.rows) == 0 {
				t.Fatal("query returned no rows")
			}
			check(t, rs)
		})
	}
}

func TestQueryResultsDoNotDependOnSessionTimeZone(t *testing.T) {
	ctx := context.Background()
	render := func(zone, sql string) string {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SET LOCAL TIME ZONE '"+zone+"'"); err != nil {
			t.Fatal(err)
		}
		rs, err := query(ctx, tx, sql)
		if err != nil {
			t.Fatalf("%s: %v", zone, err)
		}
		return renderRowset(rs)
	}
	for _, f := range queryFiles(t) {
		sql := readFile(t, f)
		t.Run(queryName(f), func(t *testing.T) {
			utc := render("UTC", sql)
			auckland := render("Pacific/Auckland", sql)
			if utc != auckland {
				t.Fatal("result differs between UTC and Pacific/Auckland session time zones")
			}
		})
	}
}

func renderRowset(rs rowset) string {
	var b strings.Builder
	for _, r := range rs.rows {
		for _, c := range rs.cols {
			switch v := r[c].(type) {
			case time.Time:
				b.WriteString(v.UTC().Format(time.RFC3339Nano))
			case pgtype.Numeric:
				f, _ := v.Float64Value()
				fmt.Fprintf(&b, "%v", f.Float64)
			default:
				fmt.Fprintf(&b, "%v", v)
			}
			b.WriteByte('|')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func planNodeTypes(t *testing.T, sql string) string {
	t.Helper()
	var plan string
	if err := pool.QueryRow(context.Background(), "EXPLAIN (FORMAT JSON) "+sql).Scan(&plan); err != nil {
		t.Fatalf("explain: %v", err)
	}
	return plan
}

func TestIndexesChangeThePlans(t *testing.T) {
	ctx := context.Background()
	files, err := filepath.Glob("explain/*.sql")
	if err != nil || len(files) != 3 {
		t.Fatalf("want 3 explain queries, got %d (%v)", len(files), err)
	}
	sort.Strings(files)
	if err := execScript(ctx, "drop_indexes.sql"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "ANALYZE"); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, f := range files {
		before[f] = planNodeTypes(t, readFile(t, f))
		if !strings.Contains(before[f], `"Seq Scan"`) || strings.Contains(before[f], "Index") {
			t.Errorf("%s without indexes: want a Seq Scan and no index node, plan:\n%s", f, before[f])
		}
	}
	if err := execScript(ctx, "indexes.sql"); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		after := planNodeTypes(t, readFile(t, f))
		if strings.Contains(after, `"Seq Scan"`) || !strings.Contains(after, "Index") {
			t.Errorf("%s with indexes: want an index-based plan and no Seq Scan, plan:\n%s", f, after)
		}
	}
}

func fingerprint(t *testing.T) string {
	t.Helper()
	statements := []string{
		`SELECT md5(string_agg(x::text, '|' ORDER BY x.id)) FROM disaster_locations x`,
		`SELECT md5(string_agg(x::text, '|' ORDER BY x.id)) FROM site_assessments x`,
		`SELECT md5(string_agg(x::text, '|' ORDER BY x.id)) FROM photos x`,
		`SELECT md5(string_agg(x::text, '|' ORDER BY x.id)) FROM domain_events x`,
		`SELECT md5(string_agg(x::text, '|' ORDER BY x.event_id, x.consumer_name)) FROM processed_events x`,
	}
	var parts []string
	for _, s := range statements {
		var h string
		if err := pool.QueryRow(context.Background(), s).Scan(&h); err != nil {
			t.Fatalf("fingerprint %q: %v", s, err)
		}
		parts = append(parts, h)
	}
	return strings.Join(parts, "/")
}

func TestSeedIsDeterministic(t *testing.T) {
	first := fingerprint(t)
	if err := seed(context.Background()); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	second := fingerprint(t)
	if first != second {
		t.Fatalf("two seed runs produced different data:\n  %s\n  %s", first, second)
	}
}

func TestSeedShape(t *testing.T) {
	within := func(what string, got, lo, hi int64) {
		t.Helper()
		if got < lo || got > hi {
			t.Errorf("%s = %d, want in [%d, %d]", what, got, lo, hi)
		}
	}
	within("locations", scalarI(t, `SELECT count(*) FROM disaster_locations`), 190, 210)
	within("site assessments", scalarI(t, `SELECT count(*) FROM site_assessments`), 1900, 2100)
	within("photos", scalarI(t, `SELECT count(*) FROM photos`), 13000, 16500)
	within("domain events", scalarI(t, `SELECT count(*) FROM domain_events`), 45000, 55000)
	within("actors", scalarI(t, `SELECT count(DISTINCT actor_sub) FROM domain_events`), 25, 25)
	within("consumers", scalarI(t, `SELECT count(DISTINCT consumer_name) FROM processed_events`), 3, 3)

	span := scalarF(t, `SELECT extract(epoch FROM max(occurred_at) - min(occurred_at)) / 86400 FROM domain_events`)
	if span < 85 || span > 90 {
		t.Errorf("event span = %.1f days, want 85..90", span)
	}

	top3 := scalarF(t, `SELECT sum(c)::float8 / (SELECT count(*) FROM domain_events)
	                    FROM (SELECT count(*) c FROM domain_events GROUP BY actor_sub ORDER BY c DESC LIMIT 3) x`)
	if top3 < 0.4 || top3 > 0.8 {
		t.Errorf("top-3 actors hold %.2f of events, want a power-user skew in 0.4..0.8", top3)
	}

	weekend := scalarF(t, `SELECT count(*) FILTER (WHERE extract(isodow FROM occurred_at AT TIME ZONE 'UTC') >= 6)::float8 / count(*)
	                        FROM domain_events`)
	if weekend > 0.10 {
		t.Errorf("weekend share = %.3f, want a clear weekday pattern (< 0.10)", weekend)
	}

	if n := scalarI(t, `SELECT count(*) FROM domain_events
	                     WHERE occurred_at >= timestamptz '2026-08-13 10:00+00' AND occurred_at < timestamptz '2026-08-13 16:00+00'`); n != 0 {
		t.Errorf("%d events inside the seeded outage window, want 0", n)
	}
	if n := scalarI(t, `SELECT max(c) FROM (SELECT count(*) c FROM photos GROUP BY site_assessment_id) x`); n > 10 {
		t.Errorf("a site has %d photos, the app caps at 10", n)
	}
	if n := scalarI(t, `SELECT count(*) FROM (SELECT 1 FROM domain_events GROUP BY aggregate_id, payload ->> 'updatedAt' HAVING count(*) > 1) x`); n == 0 {
		t.Error("no duplicate events seeded")
	}
	if n := scalarI(t, `SELECT count(*) FROM domain_events WHERE published_at IS NULL`); n == 0 {
		t.Error("no unpublished outbox rows seeded")
	}
	if n := scalarI(t, `SELECT count(*) FROM domain_events WHERE published_at IS NOT NULL AND published_at < occurred_at`); n != 0 {
		t.Errorf("%d events published before they occurred", n)
	}
	if n := scalarI(t, `SELECT count(*) FROM processed_events pe JOIN domain_events de ON de.id = pe.event_id WHERE pe.processed_at < de.published_at`); n != 0 {
		t.Errorf("%d events processed before they were published", n)
	}
}

func checkMovingAvg(t *testing.T, rs rowset) {
	var total int64
	var framesDiffer, rowsReachBeyondAWeek bool
	for _, r := range rs.rows {
		total += r.i(t, "events")
		rangeAvg, calendarAvg := r.f(t, "avg_range_7_active_days"), r.f(t, "avg_calendar_7")
		if calendarAvg > rangeAvg+0.011 {
			t.Fatalf("calendar average %v above active-day average %v", calendarAvg, rangeAvg)
		}
		if n := r.i(t, "active_days_in_window"); n < 1 || n > 7 {
			t.Fatalf("active_days_in_window = %d, want 1..7", n)
		}
		span := r.i(t, "rows_frame_span_days")
		if span < 0 {
			t.Fatalf("rows_frame_span_days = %d", span)
		}
		if span > 6 {
			rowsReachBeyondAWeek = true
		}
		if math.Abs(r.f(t, "avg_rows_7")-rangeAvg) > 0.006 {
			framesDiffer = true
		}
	}
	if want := scalarI(t, `SELECT count(*) FROM domain_events`); total != want {
		t.Errorf("sum(events) = %d, want %d", total, want)
	}
	if !rowsReachBeyondAWeek || !framesDiffer {
		t.Errorf("ROWS and RANGE frames never differ (beyondWeek=%v differ=%v): the query no longer demonstrates the point", rowsReachBeyondAWeek, framesDiffer)
	}
	step := len(rs.rows) / 30
	for i := 0; i < len(rs.rows); i += step {
		r := rs.rows[i]
		want := scalarF(t, `SELECT count(*)::float8 / 7 FROM domain_events
		                     WHERE actor_sub = $1 AND (occurred_at AT TIME ZONE 'UTC')::date BETWEEN $2::date - 6 AND $2::date`,
			r.s(t, "actor_sub"), r.ts(t, "day"))
		approx(t, fmt.Sprintf("avg_calendar_7 for %s %s", r.s(t, "actor_sub"), dateKey(r.ts(t, "day"))), r.f(t, "avg_calendar_7"), want, 0.006)
	}
}

func checkTop3(t *testing.T, rs rowset) {
	type row struct{ rn, rnk, drnk, n int64 }
	groups := map[string][]row{}
	for _, r := range rs.rows {
		loc := r.s(t, "location")
		g := row{r.i(t, "rn"), r.i(t, "rnk"), r.i(t, "drnk"), r.i(t, "assessments")}
		if !(g.drnk <= g.rnk && g.rnk <= g.rn) || g.drnk < 1 {
			t.Fatalf("%s: ranks violate dense_rank <= rank <= row_number: %+v", loc, g)
		}
		groups[loc] = append(groups[loc], g)
	}
	if want := scalarI(t, `SELECT count(DISTINCT location_id) FROM site_assessments`); int64(len(groups)) != want {
		t.Errorf("%d locations in result, want %d", len(groups), want)
	}
	ties := false
	for loc, g := range groups {
		top, tiedTop := 0, 0
		for i, x := range g {
			if x.rn != int64(i+1) {
				t.Fatalf("%s: row_number not contiguous: %+v", loc, g)
			}
			if i > 0 && x.n > g[i-1].n {
				t.Fatalf("%s: assessments not non-increasing: %+v", loc, g)
			}
			if x.rn <= 3 {
				top++
			}
			if x.rnk <= 3 {
				tiedTop++
			}
		}
		if top < 1 || top > 3 {
			t.Errorf("%s: row_number<=3 kept %d rows", loc, top)
		}
		if tiedTop > 3 {
			ties = true
		}
	}
	if !ties {
		t.Error("rank<=3 never returned more than 3 rows: no ties, so the query no longer shows the difference")
	}
	best := mustQuery(t, `SELECT l.name AS location, max(c)::bigint AS best
	                       FROM (SELECT location_id, count(*) c FROM site_assessments GROUP BY location_id, created_by) x
	                       JOIN disaster_locations l ON l.id = x.location_id GROUP BY l.name`)
	for _, b := range best.rows {
		loc := b.s(t, "location")
		if got := groups[loc][0].n; got != b.i(t, "best") {
			t.Errorf("%s: top assessor count %d, want %d", loc, got, b.i(t, "best"))
		}
	}
}

func checkTimeToFirstPhoto(t *testing.T, rs rowset) {
	lowerBoundSeconds := map[string]float64{
		"1 < 5 min": 0, "2 5-15 min": 300, "3 15-60 min": 900, "4 1-6 h": 3600, "5 >= 6 h": 21600,
	}
	var sites int64
	var share float64
	for _, r := range rs.rows {
		bucket := r.s(t, "bucket")
		sites += r.i(t, "sites")
		share += r.f(t, "share")
		if bucket == "6 no photo" {
			if want := scalarI(t, `SELECT count(*) FROM site_assessments s WHERE NOT EXISTS (SELECT 1 FROM photos p WHERE p.site_assessment_id = s.id)`); r.i(t, "sites") != want {
				t.Errorf("no-photo bucket = %d, want %d", r.i(t, "sites"), want)
			}
			continue
		}
		lo, ok := lowerBoundSeconds[bucket]
		if !ok {
			t.Fatalf("unexpected bucket %q", bucket)
		}
		if got := r.f(t, "min_ttfp_s"); got < lo {
			t.Errorf("%s: min time-to-first-photo %.1fs below bucket floor %.0fs", bucket, got, lo)
		}
	}
	if want := scalarI(t, `SELECT count(*) FROM site_assessments`); sites != want {
		t.Errorf("sites across buckets = %d, want %d", sites, want)
	}
	approx(t, "sum of shares", share, 1, 0.001)
}

func percentileCont(sorted []float64, p float64) float64 {
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

func checkLatencyPercentiles(t *testing.T, rs rowset) {
	totals := map[string]int64{}
	for _, r := range rs.rows {
		p50, p90, p99 := r.f(t, "p50_s"), r.f(t, "p90_s"), r.f(t, "p99_s")
		if p50 < 0 || p50 > p90 || p90 > p99 {
			t.Errorf("%s/%s: want 0 <= p50 <= p90 <= p99, got %v %v %v", r.s(t, "metric"), r.s(t, "segment"), p50, p90, p99)
		}
		totals[r.s(t, "metric")] += r.i(t, "n")
	}
	want := map[string]int64{
		"sync_lag":    scalarI(t, `SELECT count(*) FROM domain_events`),
		"publish_lag": scalarI(t, `SELECT count(*) FROM domain_events WHERE published_at IS NOT NULL`),
		"consume_lag": scalarI(t, `SELECT count(*) FROM processed_events`),
	}
	for metric, n := range want {
		if totals[metric] != n {
			t.Errorf("%s: n = %d, want %d", metric, totals[metric], n)
		}
	}
	lags := mustQuery(t, `SELECT extract(epoch FROM occurred_at - (payload ->> 'updatedAt')::timestamptz)::float8 AS s
	                       FROM domain_events WHERE event_type = 'photo.upserted' ORDER BY 1`)
	sorted := make([]float64, len(lags.rows))
	for i, r := range lags.rows {
		sorted[i] = r.f(t, "s")
	}
	for _, r := range rs.rows {
		if r.s(t, "metric") == "sync_lag" && r.s(t, "segment") == "photo.upserted" {
			approx(t, "photo sync p50", r.f(t, "p50_s"), percentileCont(sorted, 0.50), 0.0006)
			approx(t, "photo sync p90", r.f(t, "p90_s"), percentileCont(sorted, 0.90), 0.0006)
			approx(t, "photo sync p99", r.f(t, "p99_s"), percentileCont(sorted, 0.99), 0.0006)
		}
	}
}

type session struct {
	events     int64
	start, end time.Time
}

func sessionsFor(times []time.Time, gap time.Duration) []session {
	var out []session
	for i, ts := range times {
		if i == 0 || ts.Sub(times[i-1]) > gap {
			out = append(out, session{start: ts})
		}
		s := &out[len(out)-1]
		s.events++
		s.end = ts
	}
	return out
}

func checkSessions(t *testing.T, rs rowset) {
	var total int64
	byActor := map[string]rec{}
	for _, r := range rs.rows {
		total += r.i(t, "total_events")
		if r.i(t, "sessions") < 1 {
			t.Errorf("%s has no sessions", r.s(t, "actor_sub"))
		}
		if r.f(t, "longest_session_min") < r.f(t, "median_session_min")-0.06 {
			t.Errorf("%s: longest session shorter than median", r.s(t, "actor_sub"))
		}
		byActor[r.s(t, "actor_sub")] = r
	}
	if want := scalarI(t, `SELECT count(*) FROM domain_events`); total != want {
		t.Errorf("sum(total_events) = %d, want %d", total, want)
	}
	for _, actor := range []string{"actor-01", "actor-21"} {
		got, ok := byActor[actor]
		if !ok {
			t.Fatalf("%s missing from result", actor)
		}
		times := mustQuery(t, `SELECT occurred_at FROM domain_events WHERE actor_sub = $1 ORDER BY occurred_at, id`, actor)
		var ts []time.Time
		for _, r := range times.rows {
			ts = append(ts, r.ts(t, "occurred_at"))
		}
		sessions := sessionsFor(ts, 30*time.Minute)
		var longest time.Duration
		for _, s := range sessions {
			longest = max(longest, s.end.Sub(s.start))
		}
		if got.i(t, "sessions") != int64(len(sessions)) {
			t.Errorf("%s: sessions = %d, independent count %d", actor, got.i(t, "sessions"), len(sessions))
		}
		approx(t, actor+" longest session minutes", got.f(t, "longest_session_min"), longest.Minutes(), 0.06)
	}
}

func checkFunnel(t *testing.T, rs rowset) {
	if len(rs.rows) != 9 || rs.rows[8].s(t, "region") != "ALL" {
		t.Fatalf("want 8 regions then ALL, got %d rows", len(rs.rows))
	}
	var sum [5]int64
	for i, r := range rs.rows {
		locs, assessed, photo := r.i(t, "locations"), r.i(t, "with_assessment"), r.i(t, "with_photo")
		if !(locs >= assessed && assessed >= photo) {
			t.Errorf("%s: funnel increases: %d %d %d", r.s(t, "region"), locs, assessed, photo)
		}
		sites, withPhoto := r.i(t, "sites"), r.i(t, "sites_with_photo")
		if withPhoto > sites {
			t.Errorf("%s: sites_with_photo %d > sites %d", r.s(t, "region"), withPhoto, sites)
		}
		for _, c := range []string{"conv_location_to_assessment", "conv_assessment_to_photo", "site_photo_coverage"} {
			if v := r.f(t, c); v < 0 || v > 1 {
				t.Errorf("%s: %s = %v outside [0,1]", r.s(t, "region"), c, v)
			}
		}
		if i < 8 {
			sum[0] += locs
			sum[1] += assessed
			sum[2] += photo
			sum[3] += sites
			sum[4] += withPhoto
		}
	}
	all := rs.rows[8]
	got := [5]int64{all.i(t, "locations"), all.i(t, "with_assessment"), all.i(t, "with_photo"), all.i(t, "sites"), all.i(t, "sites_with_photo")}
	if got != sum {
		t.Errorf("ALL row %v != sum of regions %v", got, sum)
	}
	if want := scalarI(t, `SELECT count(*) FROM disaster_locations`); got[0] != want {
		t.Errorf("locations = %d, want %d", got[0], want)
	}
	if want := scalarI(t, `SELECT count(*) FROM site_assessments`); got[3] != want {
		t.Errorf("sites = %d, want %d", got[3], want)
	}
	if want := scalarI(t, `SELECT count(DISTINCT site_assessment_id) FROM photos`); got[4] != want {
		t.Errorf("sites_with_photo = %d, want %d", got[4], want)
	}
}

func checkRetention(t *testing.T, rs rowset) {
	type key struct {
		cohort string
		n      int64
	}
	type cell struct{ size, active int64 }
	got := map[key]cell{}
	for _, r := range rs.rows {
		k := key{dateKey(r.ts(t, "cohort_week")), r.i(t, "weeks_since_start")}
		c := cell{r.i(t, "cohort_size"), r.i(t, "active_actors")}
		got[k] = c
		if ret := r.f(t, "retention"); ret < 0 || ret > 1 {
			t.Errorf("%v: retention %v outside [0,1]", k, ret)
		}
		if c.active > c.size {
			t.Errorf("%v: active %d > size %d", k, c.active, c.size)
		}
		if k.n == 0 && r.f(t, "retention") != 1 {
			t.Errorf("%v: week 0 retention = %v, want 1", k, r.f(t, "retention"))
		}
		approx(t, fmt.Sprint(k, " retention"), r.f(t, "retention"), float64(c.active)/float64(c.size), 0.00006)
	}

	pairs := mustQuery(t, `SELECT DISTINCT actor_sub, date_trunc('week', occurred_at AT TIME ZONE 'UTC')::date AS week FROM domain_events`)
	weeksByActor := map[string]map[string]bool{}
	cohortOf := map[string]time.Time{}
	var lastWeek time.Time
	for _, r := range pairs.rows {
		a, w := r.s(t, "actor_sub"), r.ts(t, "week")
		if weeksByActor[a] == nil {
			weeksByActor[a] = map[string]bool{}
		}
		weeksByActor[a][dateKey(w)] = true
		if c, ok := cohortOf[a]; !ok || w.Before(c) {
			cohortOf[a] = w
		}
		if w.After(lastWeek) {
			lastWeek = w
		}
	}
	lastComplete := lastWeek.AddDate(0, 0, -7)
	want := map[key]cell{}
	size := map[string]int64{}
	for _, c := range cohortOf {
		size[dateKey(c)]++
	}
	for a, c := range cohortOf {
		if c.After(lastComplete) {
			continue
		}
		for n := int64(0); !c.AddDate(0, 0, int(7*n)).After(lastComplete); n++ {
			k := key{dateKey(c), n}
			cl := want[k]
			cl.size = size[dateKey(c)]
			if weeksByActor[a][dateKey(c.AddDate(0, 0, int(7*n)))] {
				cl.active++
			}
			want[k] = cl
		}
	}
	if len(got) != len(want) {
		t.Fatalf("result has %d cells, independent computation %d", len(got), len(want))
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%v: got %+v, independent computation %+v", k, got[k], w)
		}
	}
}

func checkRunningTotal(t *testing.T, rs rowset) {
	var photos int64
	var share, prevCum float64
	var trapVisible bool
	for i, r := range rs.rows {
		photos += r.i(t, "photos")
		share += r.f(t, "share")
		cum := r.f(t, "cum_share")
		if cum < prevCum {
			t.Fatalf("row %d: cum_share decreased %v -> %v", i, prevCum, cum)
		}
		prevCum = cum
		running, def := r.i(t, "running_photos"), r.i(t, "running_photos_default_frame")
		if running > def {
			t.Fatalf("row %d: ROWS running total %d above default-frame total %d", i, running, def)
		}
		if running < def {
			trapVisible = true
		}
		if i > 0 && r.i(t, "photos") > rs.rows[i-1].i(t, "photos") {
			t.Fatalf("row %d: not sorted by photos desc", i)
		}
	}
	if want := scalarI(t, `SELECT count(*) FROM photos`); photos != want {
		t.Errorf("sum(photos) = %d, want %d", photos, want)
	}
	approx(t, "sum of shares", share, 1, 0.01)
	approx(t, "final cum_share", prevCum, 1, 0.00006)
	if !trapVisible {
		t.Error("no ties in photo counts: default-frame trap is no longer visible")
	}
}

func checkBacklog(t *testing.T, rs rowset) {
	if len(rs.rows) != 4 {
		t.Fatalf("want 4 rows, got %d", len(rs.rows))
	}
	for _, r := range rs.rows {
		name := r.s(t, "name")
		backlog := r.i(t, "backlog")
		if backlog > r.i(t, "eligible") || r.i(t, "backlog_over_1h") > backlog {
			t.Errorf("%s: counts out of order", name)
		}
		var want int64
		var oldest float64
		if r.s(t, "kind") == "outbox" {
			want = scalarI(t, `SELECT count(*) FROM domain_events WHERE published_at IS NULL`)
			oldest = scalarF(t, `SELECT extract(epoch FROM (SELECT max(occurred_at) FROM domain_events) - min(occurred_at)) / 3600
			                      FROM domain_events WHERE published_at IS NULL`)
		} else {
			want = scalarI(t, `SELECT count(*) FROM domain_events de
			                    WHERE de.event_type = 'photo.upserted' AND de.published_at IS NOT NULL
			                      AND NOT EXISTS (SELECT 1 FROM processed_events pe WHERE pe.event_id = de.id AND pe.consumer_name = $1)`, name)
			oldest = scalarF(t, `SELECT extract(epoch FROM (SELECT max(occurred_at) FROM domain_events) - min(published_at)) / 3600
			                      FROM domain_events de
			                      WHERE de.event_type = 'photo.upserted' AND de.published_at IS NOT NULL
			                        AND NOT EXISTS (SELECT 1 FROM processed_events pe WHERE pe.event_id = de.id AND pe.consumer_name = $1)`, name)
		}
		if backlog != want {
			t.Errorf("%s: backlog = %d, independent count %d", name, backlog, want)
		}
		if backlog > 0 {
			approx(t, name+" oldest_age_h", r.f(t, "oldest_age_h"), oldest, 0.006)
			if r.f(t, "p50_age_min") < 0 {
				t.Errorf("%s: negative median age", name)
			}
		}
	}
}

func checkAnomalyDays(t *testing.T, rs rowset) {
	span := scalarI(t, `SELECT max((occurred_at AT TIME ZONE 'UTC')::date) - min((occurred_at AT TIME ZONE 'UTC')::date) + 1 FROM domain_events`)
	if int64(len(rs.rows)) != span {
		t.Fatalf("%d day rows, want %d contiguous days", len(rs.rows), span)
	}
	perDay := map[string]int64{}
	for _, r := range mustQuery(t, `SELECT (occurred_at AT TIME ZONE 'UTC')::date AS day, count(*)::bigint AS n FROM domain_events GROUP BY 1`).rows {
		perDay[dateKey(r.ts(t, "day"))] = r.i(t, "n")
	}
	var total int64
	flags := map[string]map[string]bool{}
	for i, r := range rs.rows {
		day := dateKey(r.ts(t, "day"))
		if i > 0 && r.ts(t, "day").Sub(rs.rows[i-1].ts(t, "day")) != 24*time.Hour {
			t.Fatalf("gap before %s", day)
		}
		if r.i(t, "events") != perDay[day] {
			t.Errorf("%s: events = %d, independent count %d", day, r.i(t, "events"), perDay[day])
		}
		total += r.i(t, "events")
		if r.b(t, "anomaly_trailing_28d") && r["z_trailing_28d"] != nil && r.f(t, "z_trailing_28d") < 2 {
			t.Errorf("%s: flagged but z = %v", day, r.f(t, "z_trailing_28d"))
		}
		flags[day] = map[string]bool{
			"all":      r.b(t, "anomaly_all_days"),
			"trailing": r.b(t, "anomaly_trailing_28d"),
			"weekday":  r.b(t, "anomaly_same_weekday"),
		}
	}
	if want := scalarI(t, `SELECT count(*) FROM domain_events`); total != want {
		t.Errorf("sum(events) = %d, want %d", total, want)
	}
	for _, kind := range []string{"all", "trailing", "weekday"} {
		if !flags["2026-08-28"][kind] {
			t.Errorf("planted re-sync storm 2026-08-28 not flagged by %s baseline", kind)
		}
	}
	if !flags["2026-07-23"]["trailing"] {
		t.Error("planted surge 2026-07-23 not flagged by trailing-28d baseline")
	}
}

func checkDedupe(t *testing.T, rs rowset) {
	var raw, distinct, dup int64
	var maxCopies int64
	for _, r := range rs.rows {
		raw += r.i(t, "raw_rows")
		distinct += r.i(t, "distinct_writes")
		dup += r.i(t, "duplicate_rows")
		if r.i(t, "raw_rows") != r.i(t, "distinct_writes")+r.i(t, "duplicate_rows") {
			t.Errorf("%s: raw != distinct + duplicate", r.s(t, "event_type"))
		}
		if rate := r.f(t, "duplicate_rate"); rate < 0 || rate > 1 {
			t.Errorf("%s: duplicate_rate %v outside [0,1]", r.s(t, "event_type"), rate)
		}
		maxCopies = max(maxCopies, r.i(t, "max_copies_of_one_write"))
	}
	if want := scalarI(t, `SELECT count(*) FROM domain_events`); raw != want {
		t.Errorf("raw rows = %d, want %d", raw, want)
	}
	if want := scalarI(t, `SELECT count(*) FROM (SELECT DISTINCT aggregate_id, payload ->> 'updatedAt' FROM domain_events) x`); distinct != want {
		t.Errorf("distinct writes = %d, independent count %d", distinct, want)
	}
	if dup == 0 || maxCopies < 2 {
		t.Errorf("no duplicates found (dup=%d maxCopies=%d)", dup, maxCopies)
	}
}

func checkPivot(t *testing.T, rs rowset) {
	if len(rs.rows) != 9 || rs.rows[8].s(t, "region") != "ALL" {
		t.Fatalf("want 8 regions then ALL, got %d rows", len(rs.rows))
	}
	var total, good, moderate, bad int64
	for i, r := range rs.rows {
		g, m, b, n := r.i(t, "good"), r.i(t, "moderate"), r.i(t, "bad"), r.i(t, "total")
		if g+m+b != n {
			t.Errorf("%s: %d+%d+%d != %d", r.s(t, "region"), g, m, b, n)
		}
		if p := r.f(t, "pct_bad"); p < 0 || p > 1 {
			t.Errorf("%s: pct_bad %v outside [0,1]", r.s(t, "region"), p)
		}
		approx(t, r.s(t, "region")+" pct_bad", r.f(t, "pct_bad"), float64(b)/float64(n), 0.00006)
		if i > 0 && i < 8 && r.f(t, "pct_bad") > rs.rows[i-1].f(t, "pct_bad") {
			t.Errorf("regions not ordered by pct_bad desc at %s", r.s(t, "region"))
		}
		if i < 8 {
			total += n
			good += g
			moderate += m
			bad += b
		}
	}
	all := rs.rows[8]
	if all.i(t, "total") != total || all.i(t, "good") != good || all.i(t, "moderate") != moderate || all.i(t, "bad") != bad {
		t.Error("ALL row differs from the sum of region rows")
	}
	if want := scalarI(t, `SELECT count(*) FROM photos`); total != want {
		t.Errorf("total = %d, want %d", total, want)
	}
	for cond, got := range map[string]int64{"good": good, "moderate": moderate, "bad": bad} {
		if want := scalarI(t, `SELECT count(*) FROM photos WHERE condition = $1`, cond); got != want {
			t.Errorf("%s = %d, want %d", cond, got, want)
		}
	}
}

func checkSilentHours(t *testing.T, rs rowset) {
	first := rs.rows[0]
	from := first.ts(t, "silent_from_utc")
	if want := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC); !from.Equal(want) || first.i(t, "silent_hours") != 6 {
		t.Errorf("longest silence = %v for %d h, want the seeded 6 h outage from %v", from, first.i(t, "silent_hours"), want)
	}
	for i, r := range rs.rows {
		hours := r.i(t, "silent_hours")
		if hours < 2 {
			t.Errorf("row %d: %d silent hours, HAVING requires >= 2", i, hours)
		}
		if got := r.ts(t, "silent_until_utc").Sub(r.ts(t, "silent_from_utc")); got != time.Duration(hours)*time.Hour {
			t.Errorf("row %d: window %v does not match %d hours", i, got, hours)
		}
		if i > 0 && hours > rs.rows[i-1].i(t, "silent_hours") {
			t.Errorf("row %d: not sorted by length desc", i)
		}
	}
	during := scalarI(t, `SELECT count(*) FROM domain_events WHERE occurred_at >= timestamptz '2026-08-13 10:00+00' AND occurred_at < timestamptz '2026-08-13 16:00+00'`)
	after := scalarI(t, `SELECT count(*) FROM domain_events WHERE occurred_at >= timestamptz '2026-08-13 16:00+00' AND occurred_at < timestamptz '2026-08-13 17:00+00'`)
	if during != 0 || after == 0 {
		t.Errorf("outage window has %d events (want 0), the hour after has %d (want a catch-up burst > 0)", during, after)
	}
}

func longestStreak(days []time.Time) (start time.Time, length int) {
	var runStart time.Time
	run := 0
	for i, d := range days {
		if i > 0 && d.Sub(days[i-1]) == 24*time.Hour {
			run++
		} else {
			runStart, run = d, 1
		}
		if run > length {
			start, length = runStart, run
		}
	}
	return
}

func checkStreaks(t *testing.T, rs rowset) {
	if len(rs.rows) != 25 {
		t.Fatalf("want 25 actors, got %d", len(rs.rows))
	}
	byActor := map[string]rec{}
	for i, r := range rs.rows {
		days := r.i(t, "longest_streak_days")
		if got := r.ts(t, "streak_end").Sub(r.ts(t, "streak_start")); got != time.Duration(days-1)*24*time.Hour {
			t.Errorf("%s: %v between start and end does not match %d days", r.s(t, "actor_sub"), got, days)
		}
		if i > 0 && days > rs.rows[i-1].i(t, "longest_streak_days") {
			t.Errorf("not sorted by streak desc at %s", r.s(t, "actor_sub"))
		}
		byActor[r.s(t, "actor_sub")] = r
	}
	for _, actor := range []string{"actor-01", "actor-04", "actor-21"} {
		var days []time.Time
		for _, r := range mustQuery(t, `SELECT DISTINCT (occurred_at AT TIME ZONE 'UTC')::date AS day FROM domain_events WHERE actor_sub = $1 ORDER BY 1`, actor).rows {
			days = append(days, r.ts(t, "day"))
		}
		start, length := longestStreak(days)
		got := byActor[actor]
		if got.i(t, "longest_streak_days") != int64(length) || !got.ts(t, "streak_start").Equal(start) {
			t.Errorf("%s: got %d days from %s, independent computation %d days from %s",
				actor, got.i(t, "longest_streak_days"), dateKey(got.ts(t, "streak_start")), length, dateKey(start))
		}
	}
}
