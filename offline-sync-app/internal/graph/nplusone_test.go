package graph

import (
	"testing"
)

const (
	nLocations     = 50
	assessmentsPer = 3
	photosPer      = 2
	nAssessments   = nLocations * assessmentsPer

	nestedQuery = `{ locations(first: 50) { edges { node { id siteAssessments { id photos { id } } } } } }`
	withStats   = `{ locations(first: 50) { edges { node { id stats { goodCount } siteAssessments { id photos { id } } } } } }`
)

type nestedData struct {
	Locations struct {
		Edges []struct {
			Node struct {
				SiteAssessments []struct {
					Photos []struct{ ID string }
				}
			}
		}
	}
}

func assertFullTree(t *testing.T, resp gqlResponse) {
	t.Helper()
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	var data nestedData
	decodeData(t, resp, &data)
	if got := len(data.Locations.Edges); got != nLocations {
		t.Fatalf("locations = %d, want %d", got, nLocations)
	}
	photos := 0
	for _, e := range data.Locations.Edges {
		if got := len(e.Node.SiteAssessments); got != assessmentsPer {
			t.Fatalf("assessments per location = %d, want %d", got, assessmentsPer)
		}
		for _, sa := range e.Node.SiteAssessments {
			photos += len(sa.Photos)
		}
	}
	if want := nAssessments * photosPer; photos != want {
		t.Fatalf("photos = %d, want %d", photos, want)
	}
}

func TestNPlusOne_BatchedIssuesConstantNumberOfStoreCalls(t *testing.T) {
	store := newFakeStore(nLocations, assessmentsPer, photosPer)

	resp := post(t, newSizedHandler(store, 1, nLocations, nAssessments, 1), nestedQuery, nil)

	assertFullTree(t, resp)
	for method, want := range map[string]int{
		"ListLocations":                1,
		"SiteAssessmentsByLocationIDs": 1,
		"PhotosBySiteAssessmentIDs":    1,
	} {
		if got := store.callCount(method); got != want {
			t.Errorf("%s calls = %d, want %d", method, got, want)
		}
	}
	if got := store.totalCalls(); got != 3 {
		t.Fatalf("total store calls = %d, want 3 (1 list + 1 assessments batch + 1 photos batch)", got)
	}
}

func TestNPlusOne_NaiveResolversIssueOnePlusNPlusM(t *testing.T) {
	store := newFakeStore(nLocations, assessmentsPer, photosPer)

	resp := post(t, newNaiveHandler(store), nestedQuery, nil)

	assertFullTree(t, resp)
	if got := store.callCount("ListLocations"); got != 1 {
		t.Errorf("ListLocations calls = %d, want 1", got)
	}
	if got := store.callCount("SiteAssessmentsByLocationIDs"); got != nLocations {
		t.Errorf("SiteAssessmentsByLocationIDs calls = %d, want %d (one per location)", got, nLocations)
	}
	if got := store.callCount("PhotosBySiteAssessmentIDs"); got != nAssessments {
		t.Errorf("PhotosBySiteAssessmentIDs calls = %d, want %d (one per assessment)", got, nAssessments)
	}
	if want, got := 1+nLocations+nAssessments, store.totalCalls(); got != want {
		t.Fatalf("total store calls = %d, want %d", got, want)
	}
}

func TestNPlusOne_AddingStatsAddsExactlyOneBatchedCall(t *testing.T) {
	store := newFakeStore(nLocations, assessmentsPer, photosPer)

	resp := post(t, newSizedHandler(store, 1, nLocations, nAssessments, nLocations), withStats, nil)

	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	if got := store.callCount("LocationStatsByLocationIDs"); got != 1 {
		t.Errorf("LocationStatsByLocationIDs calls = %d, want 1", got)
	}
	if got := store.totalCalls(); got != 4 {
		t.Fatalf("total store calls = %d, want 4", got)
	}
}

func TestNPlusOne_LoaderCacheIsPerRequest(t *testing.T) {
	store := newFakeStore(1, 0, 0)
	h := newTestHandler(store)
	q := `{ location(id: "` + testID(kindLocation, 0).String() + `") { id } }`

	post(t, h, q, nil)
	post(t, h, q, nil)

	if got := store.callCount("LocationsByIDs"); got != 2 {
		t.Fatalf("LocationsByIDs calls over two identical requests = %d, want 2; a cache shared across requests would give 1", got)
	}
}

func TestNPlusOne_ManyToOneBackReferenceIsBatchedToo(t *testing.T) {
	store := newFakeStore(nLocations, assessmentsPer, photosPer)
	q := `{ locations(first: 50) { edges { node { siteAssessments { location { id } } } } } }`

	resp := post(t, newSizedHandler(store, nLocations, nLocations, nAssessments, 1), q, nil)

	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	if got := store.callCount("LocationsByIDs"); got != 1 {
		t.Errorf("LocationsByIDs calls = %d, want 1 for %d assessments", got, nAssessments)
	}
}
