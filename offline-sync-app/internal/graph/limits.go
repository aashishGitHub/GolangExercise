package graph

import (
	"context"
	"strings"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

const (
	codeDepthExceeded      = "QUERY_DEPTH_EXCEEDED"
	codeTooLarge           = "QUERY_TOO_LARGE"
	codeComplexityExceeded = "COMPLEXITY_LIMIT_EXCEEDED"

	assumedAssessmentsPerLocation = 10
	maxPhotosPerAssessment        = 10

	fieldCountCap = 1 << 30
)

type Limits struct {
	MaxTokens     int
	MaxDepth      int
	MaxFields     int
	MaxComplexity int
}

var DefaultLimits = Limits{MaxTokens: 5000, MaxDepth: 8, MaxFields: 1000, MaxComplexity: 10_000}

// Without this, gqlgen answers 200 for limit violations; 422 lets gateways and dashboards see abusive clients.
func init() {
	for _, code := range []string{codeDepthExceeded, codeTooLarge, codeComplexityExceeded} {
		errcode.RegisterErrorType(code, errcode.KindProtocol)
	}
}

// photos is capped at 10 per site by UpsertPhoto; assessments per location has no cap, so 10 is an estimate.
func complexityRoot() ComplexityRoot {
	var c ComplexityRoot
	c.Query.Locations = func(child int, first *int, _ *string, _ *time.Time) int {
		return clampedPageSize(first) * child
	}
	c.Location.SiteAssessments = func(child int) int { return assumedAssessmentsPerLocation * (child + 1) }
	c.SiteAssessment.Photos = func(child int) int { return maxPhotosPerAssessment * (child + 1) }
	return c
}

// Clamped because the resolver rejects bad values only after the complexity check has already run.
func clampedPageSize(first *int) int {
	if first == nil {
		return DefaultPageSize
	}
	return min(max(*first, 1), MaxPageSize)
}

type queryLimits struct{ Limits }

func (queryLimits) ExtensionName() string                   { return "QueryLimits" }
func (queryLimits) Validate(graphql.ExecutableSchema) error { return nil }

func (l queryLimits) MutateOperationContext(_ context.Context, oc *graphql.OperationContext) *gqlerror.Error {
	got := measure(oc.Operation.SelectionSet, map[string]shape{})
	switch {
	case got.depth > l.MaxDepth:
		return limitError(codeDepthExceeded, "query depth %d exceeds the limit of %d", got.depth, l.MaxDepth)
	case got.fields > l.MaxFields:
		return limitError(codeTooLarge, "query selects %d fields after expanding fragments, limit is %d", got.fields, l.MaxFields)
	}
	return nil
}

func limitError(code, format string, args ...any) *gqlerror.Error {
	err := gqlerror.Errorf(format, args...)
	errcode.Set(err, code)
	return err
}

type shape struct{ depth, fields int }

// Fragment results are memoised: gqlgen's complexity walk re-expands every spread, so a fragment bomb
// must be rejected here, in linear time, before that walk runs.
func measure(set ast.SelectionSet, memo map[string]shape) shape {
	var out shape
	for _, sel := range set {
		switch s := sel.(type) {
		case *ast.Field:
			if strings.HasPrefix(s.Name, "__") {
				continue
			}
			child := measure(s.SelectionSet, memo)
			out.depth = max(out.depth, 1+child.depth)
			out.fields = min(out.fields+1+child.fields, fieldCountCap)
		case *ast.FragmentSpread:
			child, seen := memo[s.Name]
			if !seen {
				child = measure(s.Definition.SelectionSet, memo)
				memo[s.Name] = child
			}
			out.depth = max(out.depth, child.depth)
			out.fields = min(out.fields+child.fields, fieldCountCap)
		case *ast.InlineFragment:
			child := measure(s.SelectionSet, memo)
			out.depth = max(out.depth, child.depth)
			out.fields = min(out.fields+child.fields, fieldCountCap)
		}
	}
	return out
}
