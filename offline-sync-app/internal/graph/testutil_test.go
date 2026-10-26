package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"fieldsync/internal/db"
)

var baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func testID(kind byte, n int) uuid.UUID {
	return uuid.UUID{kind, byte(n >> 8), byte(n)}
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

const (
	kindLocation = 1
	kindSite     = 2
	kindPhoto    = 3
)

type fakeStore struct {
	mu          sync.Mutex
	locations   []db.DisasterLocation
	assessments []db.SiteAssessment
	photos      []db.Photo
	stats       []db.LocationStat

	calls  map[string]int
	failOn map[string]error
	hang   bool
}

func newFakeStore(nLocations, assessmentsPer, photosPer int) *fakeStore {
	f := &fakeStore{calls: map[string]int{}, failOn: map[string]error{}}
	siteN, photoN := 0, 0
	for i := range nLocations {
		at := baseTime.Add(time.Duration(i) * time.Minute)
		loc := db.DisasterLocation{ID: testID(kindLocation, i), Name: "loc", CreatedAt: ts(at), UpdatedAt: ts(at)}
		f.locations = append(f.locations, loc)
		f.stats = append(f.stats, db.LocationStat{LocationID: loc.ID, GoodCount: 1, ModerateCount: 2, BadCount: 3, UpdatedAt: ts(at)})
		for range assessmentsPer {
			site := db.SiteAssessment{ID: testID(kindSite, siteN), LocationID: loc.ID, Name: "site", CreatedAt: ts(at), UpdatedAt: ts(at)}
			siteN++
			f.assessments = append(f.assessments, site)
			for range photosPer {
				f.photos = append(f.photos, db.Photo{ID: testID(kindPhoto, photoN), SiteAssessmentID: site.ID, S3Key: "k", Condition: "good", CreatedAt: ts(at), UpdatedAt: ts(at)})
				photoN++
			}
		}
	}
	return f
}

func (f *fakeStore) enter(ctx context.Context, method string) error {
	f.mu.Lock()
	f.calls[method]++
	err, hang := f.failOn[method], f.hang
	f.mu.Unlock()
	if hang {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

func (f *fakeStore) callCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func (f *fakeStore) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

func (f *fakeStore) ListLocations(ctx context.Context, p ListParams) ([]db.DisasterLocation, error) {
	if err := f.enter(ctx, "ListLocations"); err != nil {
		return nil, err
	}
	var out []db.DisasterLocation
	for _, l := range f.locations {
		if p.UpdatedSince != nil && l.UpdatedAt.Time.Before(*p.UpdatedSince) {
			continue
		}
		if p.After != nil && !after(l, *p.After) {
			continue
		}
		out = append(out, l)
	}
	slices.SortFunc(out, func(a, b db.DisasterLocation) int {
		if c := a.UpdatedAt.Time.Compare(b.UpdatedAt.Time); c != 0 {
			return c
		}
		return bytes.Compare(a.ID[:], b.ID[:])
	})
	return out[:min(len(out), p.Limit)], nil
}

func after(l db.DisasterLocation, c Cursor) bool {
	if cmp := l.UpdatedAt.Time.Compare(c.UpdatedAt); cmp != 0 {
		return cmp > 0
	}
	return bytes.Compare(l.ID[:], c.ID[:]) > 0
}

func (f *fakeStore) LocationsByIDs(ctx context.Context, ids []uuid.UUID) ([]db.DisasterLocation, error) {
	if err := f.enter(ctx, "LocationsByIDs"); err != nil {
		return nil, err
	}
	return filter(f.locations, func(l db.DisasterLocation) bool { return slices.Contains(ids, l.ID) }), nil
}

func (f *fakeStore) SiteAssessmentsByLocationIDs(ctx context.Context, ids []uuid.UUID) ([]db.SiteAssessment, error) {
	if err := f.enter(ctx, "SiteAssessmentsByLocationIDs"); err != nil {
		return nil, err
	}
	return filter(f.assessments, func(a db.SiteAssessment) bool { return slices.Contains(ids, a.LocationID) }), nil
}

func (f *fakeStore) PhotosBySiteAssessmentIDs(ctx context.Context, ids []uuid.UUID) ([]db.Photo, error) {
	if err := f.enter(ctx, "PhotosBySiteAssessmentIDs"); err != nil {
		return nil, err
	}
	return filter(f.photos, func(p db.Photo) bool { return slices.Contains(ids, p.SiteAssessmentID) }), nil
}

func (f *fakeStore) LocationStatsByLocationIDs(ctx context.Context, ids []uuid.UUID) ([]db.LocationStat, error) {
	if err := f.enter(ctx, "LocationStatsByLocationIDs"); err != nil {
		return nil, err
	}
	return filter(f.stats, func(s db.LocationStat) bool { return slices.Contains(ids, s.LocationID) }), nil
}

func filter[T any](in []T, keep func(T) bool) []T {
	var out []T
	for _, v := range in {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}

func allowAll(next http.Handler) http.Handler { return next }

func testConfig() ServerConfig {
	cfg := DefaultServerConfig()
	cfg.Loader = LoaderConfig{Wait: 25 * time.Millisecond, MaxBatch: 1000}
	return cfg
}

func newTestHandler(store Store, mutate ...func(*ServerConfig)) http.Handler {
	cfg := testConfig()
	for _, m := range mutate {
		m(&cfg)
	}
	return NewHandler(store, allowAll, cfg)
}

type gqlError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path"`
	Extensions map[string]any `json:"extensions"`
}

type gqlResponse struct {
	Status int
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

func post(t *testing.T, h http.Handler, query string, vars map[string]any) gqlResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	resp := gqlResponse{Status: rec.Code}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return resp
}

func decodeData(t *testing.T, resp gqlResponse, into any) {
	t.Helper()
	if err := json.Unmarshal(resp.Data, into); err != nil {
		t.Fatalf("decode data %s: %v", resp.Data, err)
	}
}

func hasErrorCode(resp gqlResponse, code string) bool {
	return slices.ContainsFunc(resp.Errors, func(e gqlError) bool { return e.Extensions["code"] == code })
}

type naiveLoader[K comparable, V any] struct{ fetch BatchFunc[K, V] }

func (n naiveLoader[K, V]) Load(ctx context.Context, key K) (V, error) {
	m, err := n.fetch(ctx, []K{key})
	if err != nil {
		var zero V
		return zero, err
	}
	return m[key], nil
}

func naiveLoaders(s Store) *Loaders {
	return &Loaders{
		Locations:       naiveLoader[uuid.UUID, *db.DisasterLocation]{locationsBatch(s)},
		SiteAssessments: naiveLoader[uuid.UUID, []db.SiteAssessment]{siteAssessmentsBatch(s)},
		Photos:          naiveLoader[uuid.UUID, []db.Photo]{photosBatch(s)},
		Stats:           naiveLoader[uuid.UUID, *db.LocationStat]{statsBatch(s)},
	}
}

func newNaiveHandler(store Store) http.Handler {
	return newTestHandler(store, func(c *ServerConfig) { c.NewLoaders = naiveLoaders })
}

// A timer window is non-deterministic under CPU contention, so tests flush each loader when its expected
// key count has arrived; the 5s timer only turns a wrong expectation into a failed assertion instead of a hang.
func sizedLoaders(s Store, locations, assessments, photos, stats int) *Loaders {
	size := func(n int) LoaderConfig { return LoaderConfig{Wait: 5 * time.Second, MaxBatch: n} }
	return &Loaders{
		Locations:       NewDataLoader(locationsBatch(s), size(locations)),
		SiteAssessments: NewDataLoader(siteAssessmentsBatch(s), size(assessments)),
		Photos:          NewDataLoader(photosBatch(s), size(photos)),
		Stats:           NewDataLoader(statsBatch(s), size(stats)),
	}
}

func newSizedHandler(store Store, locations, assessments, photos, stats int) http.Handler {
	return newTestHandler(store, func(c *ServerConfig) {
		c.NewLoaders = func(s Store) *Loaders { return sizedLoaders(s, locations, assessments, photos, stats) }
	})
}
