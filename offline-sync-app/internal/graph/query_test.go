package graph

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type pageData struct {
	Locations struct {
		Edges []struct {
			Cursor string
			Node   struct{ ID string }
		}
		PageInfo struct {
			HasNextPage bool
			EndCursor   *string
		}
	}
}

const pageQuery = `query($first: Int, $after: String, $since: Time) {
  locations(first: $first, after: $after, updatedSince: $since) {
    edges { cursor node { id } }
    pageInfo { hasNextPage endCursor }
  }
}`

func fetchPage(t *testing.T, h http.Handler, vars map[string]any) pageData {
	t.Helper()
	resp := post(t, h, pageQuery, vars)
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	var data pageData
	decodeData(t, resp, &data)
	return data
}

func TestLocations_KeysetPaginationWalksEveryRowOnce(t *testing.T) {
	store := newFakeStore(45, 0, 0)
	h := newTestHandler(store)

	var seen []string
	var pageSizes []int
	vars := map[string]any{"first": 20}
	for {
		page := fetchPage(t, h, vars)
		pageSizes = append(pageSizes, len(page.Locations.Edges))
		for _, e := range page.Locations.Edges {
			seen = append(seen, e.Node.ID)
		}
		if !page.Locations.PageInfo.HasNextPage {
			break
		}
		vars["after"] = *page.Locations.PageInfo.EndCursor
	}

	if want := []int{20, 20, 5}; len(pageSizes) != 3 || pageSizes[0] != want[0] || pageSizes[1] != want[1] || pageSizes[2] != want[2] {
		t.Fatalf("page sizes = %v, want %v", pageSizes, want)
	}
	if len(seen) != 45 {
		t.Fatalf("saw %d ids, want 45", len(seen))
	}
	for i, id := range seen {
		if want := testID(kindLocation, i).String(); id != want {
			t.Fatalf("row %d = %s, want %s (updated_at order)", i, id, want)
		}
	}
}

func TestLocations_EndCursorIsLastEdgeCursor(t *testing.T) {
	page := fetchPage(t, newTestHandler(newFakeStore(5, 0, 0)), map[string]any{"first": 3})

	if got, want := *page.Locations.PageInfo.EndCursor, page.Locations.Edges[2].Cursor; got != want {
		t.Fatalf("endCursor = %q, want last edge cursor %q", got, want)
	}
}

func TestLocations_EmptyResultHasNoEndCursor(t *testing.T) {
	page := fetchPage(t, newTestHandler(newFakeStore(0, 0, 0)), nil)

	if len(page.Locations.Edges) != 0 || page.Locations.PageInfo.HasNextPage || page.Locations.PageInfo.EndCursor != nil {
		t.Fatalf("page = %+v, want empty with no next page and null endCursor", page)
	}
}

func TestLocations_UpdatedSinceFiltersOlderRows(t *testing.T) {
	store := newFakeStore(10, 0, 0)
	since := baseTime.Add(7 * time.Minute).Format(time.RFC3339)

	page := fetchPage(t, newTestHandler(store), map[string]any{"first": 50, "since": since})

	if got := len(page.Locations.Edges); got != 3 {
		t.Fatalf("rows since minute 7 = %d, want 3 (minutes 7, 8, 9)", got)
	}
}

func TestLocations_DefaultsToTwentyRows(t *testing.T) {
	page := fetchPage(t, newTestHandler(newFakeStore(30, 0, 0)), nil)

	if got := len(page.Locations.Edges); got != DefaultPageSize || !page.Locations.PageInfo.HasNextPage {
		t.Fatalf("rows = %d hasNext = %v, want %d and true", got, page.Locations.PageInfo.HasNextPage, DefaultPageSize)
	}
}

func TestLocations_BadInputIsAUserErrorNotAnInternalOne(t *testing.T) {
	store := newFakeStore(3, 0, 0)
	h := newTestHandler(store)

	for name, vars := range map[string]map[string]any{
		"first zero":            {"first": 0},
		"first over max":        {"first": MaxPageSize + 1},
		"first negative":        {"first": -5},
		"garbage cursor":        {"after": "not-a-cursor"},
		"cursor of wrong shape": {"after": base64.RawURLEncoding.EncodeToString([]byte("1|not-a-uuid"))},
	} {
		resp := post(t, h, pageQuery, vars)
		if !hasErrorCode(resp, codeBadInput) {
			t.Errorf("%s: errors = %+v, want code %s", name, resp.Errors, codeBadInput)
		}
	}
	if got := store.totalCalls(); got != 0 {
		t.Errorf("store calls = %d, want 0: bad input must be rejected before touching the database", got)
	}
}

func TestLocation_ByID(t *testing.T) {
	store := newFakeStore(3, 2, 1)
	h := newTestHandler(store)
	q := `query($id: ID!) { location(id: $id) { id name siteAssessments { id photos { condition } } stats { goodCount moderateCount badCount } } }`

	resp := post(t, h, q, map[string]any{"id": testID(kindLocation, 1).String()})

	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	var data struct {
		Location struct {
			ID              string
			SiteAssessments []struct{ Photos []struct{ Condition string } }
			Stats           struct{ GoodCount, ModerateCount, BadCount int }
		}
	}
	decodeData(t, resp, &data)
	if data.Location.ID != testID(kindLocation, 1).String() || len(data.Location.SiteAssessments) != 2 {
		t.Fatalf("location = %+v", data.Location)
	}
	if got := data.Location.SiteAssessments[0].Photos[0].Condition; got != "GOOD" {
		t.Errorf("photo condition = %q, want enum value GOOD", got)
	}
	if s := data.Location.Stats; s.GoodCount != 1 || s.ModerateCount != 2 || s.BadCount != 3 {
		t.Errorf("stats = %+v", s)
	}
}

func TestLocation_UnknownIDIsNullWithoutError(t *testing.T) {
	resp := post(t, newTestHandler(newFakeStore(1, 0, 0)),
		`query($id: ID!) { location(id: $id) { id } }`, map[string]any{"id": testID(kindLocation, 999).String()})

	if len(resp.Errors) > 0 || string(resp.Data) != `{"location":null}` {
		t.Fatalf("data = %s errors = %+v, want location:null and no errors", resp.Data, resp.Errors)
	}
}

func TestLocation_MalformedIDIsAUserErrorAndNeverReachesTheStore(t *testing.T) {
	store := newFakeStore(1, 0, 0)

	resp := post(t, newTestHandler(store), `query($id: ID!) { location(id: $id) { id } }`, map[string]any{"id": "nope"})

	if !hasErrorCode(resp, codeBadInput) || store.totalCalls() != 0 {
		t.Fatalf("errors = %+v calls = %d, want %s and zero store calls", resp.Errors, store.totalCalls(), codeBadInput)
	}
}

func TestLocation_MissingStatsRowIsNull(t *testing.T) {
	store := newFakeStore(1, 0, 0)
	store.stats = nil

	resp := post(t, newTestHandler(store), `{ locations { edges { node { stats { goodCount } } } } }`, nil)

	if len(resp.Errors) > 0 || string(resp.Data) != `{"locations":{"edges":[{"node":{"stats":null}}]}}` {
		t.Fatalf("data = %s errors = %+v", resp.Data, resp.Errors)
	}
}

func TestIntrospection_DisabledByDefaultEnabledOnRequest(t *testing.T) {
	store := newFakeStore(0, 0, 0)

	off := post(t, newTestHandler(store), `{ __schema { queryType { name } } }`, nil)
	on := post(t, newTestHandler(store, func(c *ServerConfig) { c.Introspection = true }), `{ __schema { queryType { name } } }`, nil)

	if len(off.Errors) == 0 {
		t.Error("introspection answered although disabled")
	}
	if len(on.Errors) > 0 || string(on.Data) != `{"__schema":{"queryType":{"name":"Query"}}}` {
		t.Errorf("enabled introspection: data = %s errors = %+v", on.Data, on.Errors)
	}
}

func TestOnlyPostIsServed(t *testing.T) {
	h := newTestHandler(newFakeStore(0, 0, 0))
	req := httptest.NewRequest(http.MethodGet, "/graphql?query=%7Blocations%7Bedges%7Bcursor%7D%7D%7D", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET status = %d, want 400 (transport not supported)", rec.Code)
	}
}
