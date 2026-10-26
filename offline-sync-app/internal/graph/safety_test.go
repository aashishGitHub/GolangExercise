package graph

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vektah/gqlparser/v2"
)

func cyclicQuery(hops int) string {
	var b strings.Builder
	b.WriteString("{ location(id: \"" + testID(kindLocation, 0).String() + "\") ")
	for range hops {
		b.WriteString("{ siteAssessments { location ")
	}
	b.WriteString("{ id }")
	b.WriteString(strings.Repeat(" } }", hops))
	b.WriteString(" }")
	return b.String()
}

func TestDepthLimit_RejectsAbusiveCyclicQuery(t *testing.T) {
	store := newFakeStore(3, 2, 1)

	resp := post(t, newTestHandler(store), cyclicQuery(20), nil)

	if resp.Status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.Status)
	}
	if !hasErrorCode(resp, codeDepthExceeded) {
		t.Fatalf("errors = %+v, want code %s", resp.Errors, codeDepthExceeded)
	}
	if !strings.Contains(resp.Errors[0].Message, "exceeds the limit of 8") {
		t.Errorf("message = %q, want it to state the limit", resp.Errors[0].Message)
	}
	if got := store.totalCalls(); got != 0 {
		t.Errorf("store calls = %d, want 0: rejected before execution", got)
	}
}

func TestDepthLimit_AllowsDeepestLegitimateQuery(t *testing.T) {
	q := `{ locations(first: 1) { edges { node { siteAssessments { location { stats { goodCount } } } } } } }`

	resp := post(t, newTestHandler(newFakeStore(1, 1, 1)), q, nil)

	if len(resp.Errors) > 0 {
		t.Fatalf("legitimate depth-7 query rejected: %+v", resp.Errors)
	}
}

func TestDepthLimit_BoundaryIsInclusive(t *testing.T) {
	q := `{ locations(first: 1) { edges { node { id } } } }`
	atLimit := func(c *ServerConfig) { c.Limits.MaxDepth = 4 }
	below := func(c *ServerConfig) { c.Limits.MaxDepth = 3 }

	if resp := post(t, newTestHandler(newFakeStore(1, 0, 0), atLimit), q, nil); len(resp.Errors) > 0 {
		t.Errorf("depth 4 with limit 4 rejected: %+v", resp.Errors)
	}
	if resp := post(t, newTestHandler(newFakeStore(1, 0, 0), below), q, nil); !hasErrorCode(resp, codeDepthExceeded) {
		t.Errorf("depth 4 with limit 3 accepted: %+v", resp.Errors)
	}
}

func TestDepthLimit_FragmentsCountTowardsDepth(t *testing.T) {
	q := `
	  fragment A on Location { siteAssessments { ...B } }
	  fragment B on SiteAssessment { location { siteAssessments { location { siteAssessments { id } } } } }
	  { locations(first: 1) { edges { node { ...A } } } }`

	resp := post(t, newTestHandler(newFakeStore(1, 1, 1)), q, nil)

	if !hasErrorCode(resp, codeDepthExceeded) {
		t.Fatalf("errors = %+v, want a depth error; hiding depth in fragments must not bypass the limit", resp.Errors)
	}
}

func fragmentBomb(levels, fanout int) string {
	var b strings.Builder
	for i := range levels {
		fmt.Fprintf(&b, "fragment F%d on Location { ", i)
		for j := range fanout {
			next := fmt.Sprintf("...F%d", i+1)
			if i == levels-1 {
				next = "id"
			}
			fmt.Fprintf(&b, "a%d: siteAssessments { id location { %s } } ", j, next)
		}
		b.WriteString("}\n")
	}
	b.WriteString(`{ location(id: "` + testID(kindLocation, 0).String() + `") { ...F0 } }`)
	return b.String()
}

func TestMeasure_MemoisesFragmentsSoABombIsLinear(t *testing.T) {
	schema := NewExecutableSchema(Config{}).Schema()
	doc, errs := gqlparser.LoadQuery(schema, fragmentBomb(40, 2))
	if errs != nil {
		t.Fatalf("load bomb: %v", errs)
	}

	done := make(chan shape, 1)
	go func() { done <- measure(doc.Operations[0].SelectionSet, map[string]shape{}) }()

	select {
	case got := <-done:
		if got.fields != fieldCountCap {
			t.Errorf("fields = %d, want saturation at %d", got.fields, fieldCountCap)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("measure did not finish: fragments are being re-expanded instead of memoised")
	}
}

func TestFieldLimit_RejectsFragmentExpansionBeforeComplexityWalk(t *testing.T) {
	store := newFakeStore(1, 1, 1)
	q := fragmentBomb(4, 8)

	start := time.Now()
	resp := post(t, newTestHandler(store, func(c *ServerConfig) { c.Limits.MaxDepth = 100 }), q, nil)

	if !hasErrorCode(resp, codeTooLarge) {
		t.Fatalf("errors = %+v, want code %s", resp.Errors, codeTooLarge)
	}
	if resp.Status != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", resp.Status)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("rejection took %v, want well under a second", d)
	}
	if got := store.totalCalls(); got != 0 {
		t.Errorf("store calls = %d, want 0", got)
	}
}

func TestComplexityLimit_RejectsWideNestedPage(t *testing.T) {
	store := newFakeStore(1, 1, 1)
	q := `{ locations(first: 100) { edges { node { siteAssessments { photos { id } } } } } }`

	resp := post(t, newTestHandler(store), q, nil)

	if resp.Status != http.StatusUnprocessableEntity || !hasErrorCode(resp, codeComplexityExceeded) {
		t.Fatalf("status = %d errors = %+v, want 422 %s", resp.Status, resp.Errors, codeComplexityExceeded)
	}
	if !strings.Contains(resp.Errors[0].Message, "exceeds the limit of 10000") {
		t.Errorf("message = %q, want it to state complexity and limit", resp.Errors[0].Message)
	}
	if store.totalCalls() != 0 {
		t.Errorf("store calls = %d, want 0", store.totalCalls())
	}
}

func TestComplexityLimit_AllowsHalfSizedPageOfTheSameShape(t *testing.T) {
	q := `{ locations(first: 50) { edges { node { siteAssessments { photos { id } } } } } }`

	resp := post(t, newTestHandler(newFakeStore(1, 1, 1)), q, nil)

	if len(resp.Errors) > 0 {
		t.Fatalf("first:50 rejected: %+v", resp.Errors)
	}
}

func aliasedPages(n int) string {
	var b strings.Builder
	b.WriteString("{ ")
	for i := range n {
		fmt.Fprintf(&b, "p%d: locations(first: 100) { edges { node { siteAssessments { id } } } } ", i)
	}
	b.WriteString("}")
	return b.String()
}

func TestComplexityLimit_AliasesMultiplyCost(t *testing.T) {
	h := newTestHandler(newFakeStore(1, 1, 1))

	if resp := post(t, h, aliasedPages(1), nil); len(resp.Errors) > 0 {
		t.Fatalf("one page rejected: %+v", resp.Errors)
	}
	if resp := post(t, h, aliasedPages(10), nil); !hasErrorCode(resp, codeComplexityExceeded) {
		t.Fatalf("errors = %+v, want %s: aliasing one cheap field must not dodge the limit", resp.Errors, codeComplexityExceeded)
	}
}

func TestComplexityLimit_HugeFirstIsClampedNotOverflowed(t *testing.T) {
	if got := clampedPageSize(ptr(1 << 40)); got != MaxPageSize {
		t.Errorf("clampedPageSize(huge) = %d, want %d", got, MaxPageSize)
	}
	if got := clampedPageSize(ptr(-3)); got != 1 {
		t.Errorf("clampedPageSize(-3) = %d, want 1", got)
	}
	if got := clampedPageSize(nil); got != DefaultPageSize {
		t.Errorf("clampedPageSize(nil) = %d, want %d", got, DefaultPageSize)
	}
}

func ptr[T any](v T) *T { return &v }

func TestTokenLimit_RejectsOversizedDocumentsAtParse(t *testing.T) {
	store := newFakeStore(1, 0, 0)
	q := "{ " + strings.Repeat("locations { edges { cursor } } ", 1000) + "}"

	resp := post(t, newTestHandler(store), q, nil)

	if resp.Status != http.StatusUnprocessableEntity || len(resp.Errors) != 1 || !strings.Contains(resp.Errors[0].Message, "token limit of 5000") {
		t.Fatalf("status = %d errors = %+v, want a 422 token-limit parse error", resp.Status, resp.Errors)
	}
	if store.totalCalls() != 0 {
		t.Errorf("store calls = %d, want 0", store.totalCalls())
	}
}

func TestPartialResults_FailingChildFieldKeepsSiblingsAndReportsPath(t *testing.T) {
	store := newFakeStore(3, 2, 1)
	store.failOn["PhotosBySiteAssessmentIDs"] = errors.New("pq: relation photos is locked, password=hunter2")
	q := `{ locations(first: 3) { edges { node { id siteAssessments { id photos { id } } } } } }`

	resp := post(t, newSizedHandler(store, 1, 3, 6, 1), q, nil)

	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200: field errors are not transport errors", resp.Status)
	}
	var data struct {
		Locations struct {
			Edges []struct {
				Node struct {
					ID              string
					SiteAssessments []struct {
						ID     string
						Photos []struct{ ID string }
					}
				}
			}
		}
	}
	decodeData(t, resp, &data)
	if len(data.Locations.Edges) != 3 {
		t.Fatalf("locations = %d, want 3: parent data must survive a child failure", len(data.Locations.Edges))
	}
	for _, e := range data.Locations.Edges {
		if len(e.Node.SiteAssessments) != 2 {
			t.Fatalf("assessments = %d, want 2: siblings of the failing field must survive", len(e.Node.SiteAssessments))
		}
		for _, sa := range e.Node.SiteAssessments {
			if sa.Photos != nil {
				t.Errorf("photos = %v, want null for the failed field", sa.Photos)
			}
		}
	}

	if len(resp.Errors) != 6 {
		t.Fatalf("errors = %d, want 6: the shared batch failure surfaces once per assessment", len(resp.Errors))
	}
	for _, e := range resp.Errors {
		if len(e.Path) != 7 || e.Path[0] != "locations" || e.Path[len(e.Path)-1] != "photos" {
			t.Errorf("error path = %v, want locations.edges[i].node.siteAssessments[j].photos", e.Path)
		}
		if e.Message != "internal error" || strings.Contains(e.Message, "hunter2") {
			t.Errorf("message = %q, want the masked message", e.Message)
		}
		if e.Extensions["code"] != "INTERNAL_SERVER_ERROR" {
			t.Errorf("code = %v, want INTERNAL_SERVER_ERROR", e.Extensions["code"])
		}
	}
	if got := store.callCount("PhotosBySiteAssessmentIDs"); got != 1 {
		t.Errorf("photo batch calls = %d, want 1: failure is one batch, not six fetches", got)
	}
}

func TestPartialResults_FailingStatsDoNotLoseTheLocation(t *testing.T) {
	store := newFakeStore(2, 0, 0)
	store.failOn["LocationStatsByLocationIDs"] = errors.New("boom")

	resp := post(t, newTestHandler(store), `{ locations { edges { node { id stats { goodCount } } } } }`, nil)

	var data struct {
		Locations struct {
			Edges []struct{ Node struct{ ID string } }
		}
	}
	decodeData(t, resp, &data)
	if len(data.Locations.Edges) != 2 || len(resp.Errors) != 2 {
		t.Fatalf("edges = %d errors = %d, want 2 and 2", len(data.Locations.Edges), len(resp.Errors))
	}
}

func TestRootFailureNullsDataAndReportsOneMaskedError(t *testing.T) {
	store := newFakeStore(2, 0, 0)
	store.failOn["ListLocations"] = errors.New("connection refused to 10.0.0.5:5432")

	resp := post(t, newTestHandler(store), `{ locations { edges { cursor } } }`, nil)

	if len(resp.Errors) != 1 || resp.Errors[0].Message != "internal error" {
		t.Fatalf("errors = %+v, want exactly one masked error", resp.Errors)
	}
	if string(resp.Data) != "null" {
		t.Errorf("data = %s, want null because locations is non-null", resp.Data)
	}
}

func TestRequestTimeoutBoundsASlowStore(t *testing.T) {
	store := newFakeStore(1, 0, 0)
	store.hang = true
	h := newTestHandler(store, func(c *ServerConfig) { c.RequestTimeout = 50 * time.Millisecond })

	start := time.Now()
	resp := post(t, h, `{ locations { edges { cursor } } }`, nil)

	if len(resp.Errors) == 0 {
		t.Fatal("want an error from the timed-out store call")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("request took %v, want it cut off near the 50ms timeout", d)
	}
}
