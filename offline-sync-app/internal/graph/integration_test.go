//go:build integration

// Run with: go test -tags=integration -run Integration ./internal/graph/
// Needs a Postgres with migrations 000001+000002 applied in a database named fieldsync_graphql
// (override the DSN with GRAPHQL_TEST_DATABASE_URL). The test truncates the four data tables.
package graph

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/db"
)

const (
	integrationDB   = "fieldsync_graphql"
	defaultTestDSN  = "postgres://fieldsync:fieldsync@localhost:5432/" + integrationDB + "?sslmode=disable"
	tieGroupSize    = 5
	integrationPage = 7
)

type countingConn struct {
	db.DBTX
	mu         sync.Mutex
	statements []string
}

func (c *countingConn) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.mu.Lock()
	c.statements = append(c.statements, sql)
	c.mu.Unlock()
	return c.DBTX.Query(ctx, sql, args...)
}

func (c *countingConn) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.statements)
}

func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRAPHQL_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = defaultTestDSN
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if name := pool.Config().ConnConfig.Database; name != integrationDB {
		t.Fatalf("refusing to truncate tables in database %q: this test only runs against %q", name, integrationDB)
	}
	return pool
}

// Locations share updated_at in groups of tieGroupSize so keyset pagination must break ties on id.
func seedIntegration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	exec("TRUNCATE photos, site_assessments, location_stats, disaster_locations")

	siteN, photoN := 0, 0
	for i := range nLocations {
		at := baseTime.Add(time.Duration(i/tieGroupSize) * time.Minute)
		locID := testID(kindLocation, i)
		exec(`INSERT INTO disaster_locations (id, name, latitude, longitude, created_at, updated_at, created_by) VALUES ($1, $2, 1, 2, $3, $3, 'seed')`,
			locID, "loc", at)
		exec(`INSERT INTO location_stats (location_id, good_count, moderate_count, bad_count) VALUES ($1, 1, 2, 3)`, locID)
		for range assessmentsPer {
			siteID := testID(kindSite, siteN)
			siteN++
			exec(`INSERT INTO site_assessments (id, location_id, name, created_at, updated_at, created_by) VALUES ($1, $2, 'site', $3, $3, 'seed')`, siteID, locID, at)
			for range photosPer {
				exec(`INSERT INTO photos (id, site_assessment_id, s3_key, latitude, longitude, condition, created_at, updated_at, created_by) VALUES ($1, $2, 'k', 1, 2, 'bad', $3, $3, 'seed')`,
					testID(kindPhoto, photoN), siteID, at)
				photoN++
			}
		}
	}
}

type batchSizes struct{ locations, assessments, photos, stats int }

func newIntegrationHandler(t *testing.T, n batchSizes) (*countingConn, *pgxpool.Pool, func(query string, vars map[string]any) gqlResponse) {
	t.Helper()
	pool := integrationPool(t)
	seedIntegration(t, pool)
	conn := &countingConn{DBTX: pool}
	store := NewPostgresStore(conn)
	h := newSizedHandler(store, n.locations, n.assessments, n.photos, n.stats)
	return conn, pool, func(query string, vars map[string]any) gqlResponse { return post(t, h, query, vars) }
}

func TestIntegration_NestedQueryIssuesConstantNumberOfSQLStatements(t *testing.T) {
	conn, _, run := newIntegrationHandler(t, batchSizes{assessments: nLocations, photos: nAssessments})

	resp := run(nestedQuery, nil)

	assertFullTree(t, resp)
	if got := conn.count(); got != 3 {
		t.Fatalf("SQL statements = %d, want 3 (list, assessments ANY(...), photos ANY(...)); statements: %q", got, conn.statements)
	}
	t.Logf("real Postgres, %d locations x %d assessments x %d photos: %d SQL statements", nLocations, assessmentsPer, photosPer, conn.count())
}

func TestIntegration_StatsAndBackReferenceAreBatchedToo(t *testing.T) {
	conn, _, run := newIntegrationHandler(t, batchSizes{locations: nLocations, assessments: nLocations, stats: nLocations})
	q := `{ locations(first: 50) { edges { node { stats { goodCount moderateCount badCount } siteAssessments { location { id } } } } } }`

	resp := run(q, nil)

	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	if got := conn.count(); got != 4 {
		t.Fatalf("SQL statements = %d, want 4 (list, stats, assessments, locations by id); statements: %q", got, conn.statements)
	}
}

func walk(t *testing.T, run func(string, map[string]any) gqlResponse, between func(page int)) []string {
	t.Helper()
	var ids []string
	vars := map[string]any{"first": integrationPage}
	for page := 0; ; page++ {
		resp := run(pageQuery, vars)
		if len(resp.Errors) > 0 {
			t.Fatalf("page %d errors: %+v", page, resp.Errors)
		}
		var data pageData
		decodeData(t, resp, &data)
		for _, e := range data.Locations.Edges {
			ids = append(ids, e.Node.ID)
		}
		if !data.Locations.PageInfo.HasNextPage {
			return ids
		}
		vars["after"] = *data.Locations.PageInfo.EndCursor
		if between != nil {
			between(page)
		}
	}
}

func TestIntegration_KeysetPaginationBreaksTimestampTiesOnID(t *testing.T) {
	_, _, run := newIntegrationHandler(t, batchSizes{})

	got := walk(t, run, nil)

	if len(got) != nLocations {
		t.Fatalf("saw %d rows, want %d: ties across page boundaries lost or duplicated rows", len(got), nLocations)
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("row %s returned twice", id)
		}
		seen[id] = true
	}
	for i, id := range got {
		if id != testID(kindLocation, i).String() {
			t.Fatalf("row %d = %s, want %s: order must be (updated_at, id)", i, id, testID(kindLocation, i))
		}
	}
}

func TestIntegration_RowUpdatedMidWalkReappearsAndIsNeverSkipped(t *testing.T) {
	_, pool, run := newIntegrationHandler(t, batchSizes{})
	touched := testID(kindLocation, 0)

	got := walk(t, run, func(page int) {
		if page != 0 {
			return
		}
		_, err := pool.Exec(context.Background(), "UPDATE disaster_locations SET updated_at = $1 WHERE id = $2", baseTime.Add(24*time.Hour), touched)
		if err != nil {
			t.Fatalf("touch row: %v", err)
		}
	})

	counts := map[string]int{}
	for _, id := range got {
		counts[id]++
	}
	if len(counts) != nLocations {
		t.Fatalf("distinct rows = %d, want %d: a row was skipped", len(counts), nLocations)
	}
	if counts[touched.String()] != 2 {
		t.Fatalf("updated row seen %d times, want 2: at-least-once delivery, consumer dedups by id", counts[touched.String()])
	}
}

func TestIntegration_UpdatedSinceUsesInclusiveLowerBound(t *testing.T) {
	_, _, run := newIntegrationHandler(t, batchSizes{})
	since := baseTime.Add(8 * time.Minute).Format(time.RFC3339)

	resp := run(pageQuery, map[string]any{"first": 100, "since": since})

	var data pageData
	decodeData(t, resp, &data)
	if want := nLocations - 8*tieGroupSize; len(data.Locations.Edges) != want {
		t.Fatalf("rows since minute 8 = %d, want %d (groups 8 and 9)", len(data.Locations.Edges), want)
	}
}

func TestIntegration_LocationByIDAndUnknownID(t *testing.T) {
	_, _, run := newIntegrationHandler(t, batchSizes{locations: 1, assessments: 1, photos: assessmentsPer, stats: 1})
	q := `query($id: ID!) { location(id: $id) { name stats { badCount } siteAssessments { photos { condition } } } }`

	found := run(q, map[string]any{"id": testID(kindLocation, 3).String()})
	missing := run(q, map[string]any{"id": testID(kindLocation, 4000).String()})

	if len(found.Errors) > 0 || len(missing.Errors) > 0 {
		t.Fatalf("errors: %+v / %+v", found.Errors, missing.Errors)
	}
	if string(missing.Data) != `{"location":null}` {
		t.Errorf("unknown id data = %s, want location:null", missing.Data)
	}
	var data struct {
		Location struct {
			Stats           struct{ BadCount int }
			SiteAssessments []struct{ Photos []struct{ Condition string } }
		}
	}
	decodeData(t, found, &data)
	if data.Location.Stats.BadCount != 3 || len(data.Location.SiteAssessments) != assessmentsPer || data.Location.SiteAssessments[0].Photos[0].Condition != "BAD" {
		t.Errorf("location = %+v", data.Location)
	}
}
