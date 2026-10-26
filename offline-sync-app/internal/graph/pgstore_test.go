package graph

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestListLocationsSQL(t *testing.T) {
	since := baseTime
	id := uuid.UUID{9}
	cur := &Cursor{UpdatedAt: baseTime.Add(time.Hour), ID: id}

	tests := []struct {
		name     string
		params   ListParams
		wantSQL  string
		wantArgs []any
	}{
		{
			name:     "first page, no filter",
			params:   ListParams{Limit: 21},
			wantSQL:  "SELECT " + locationCols + " FROM disaster_locations ORDER BY updated_at, id LIMIT $1",
			wantArgs: []any{21},
		},
		{
			name:     "updatedSince only",
			params:   ListParams{Limit: 21, UpdatedSince: &since},
			wantSQL:  "SELECT " + locationCols + " FROM disaster_locations WHERE updated_at >= $1 ORDER BY updated_at, id LIMIT $2",
			wantArgs: []any{since, 21},
		},
		{
			name:     "cursor only",
			params:   ListParams{Limit: 21, After: cur},
			wantSQL:  "SELECT " + locationCols + " FROM disaster_locations WHERE (updated_at, id) > ($1, $2) ORDER BY updated_at, id LIMIT $3",
			wantArgs: []any{cur.UpdatedAt, id, 21},
		},
		{
			name:     "both",
			params:   ListParams{Limit: 21, UpdatedSince: &since, After: cur},
			wantSQL:  "SELECT " + locationCols + " FROM disaster_locations WHERE updated_at >= $1 AND (updated_at, id) > ($2, $3) ORDER BY updated_at, id LIMIT $4",
			wantArgs: []any{since, cur.UpdatedAt, id, 21},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSQL, gotArgs := listLocationsSQL(tt.params)
			if gotSQL != tt.wantSQL {
				t.Errorf("sql =\n  %s\nwant\n  %s", gotSQL, tt.wantSQL)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Errorf("args = %v, want %v", gotArgs, tt.wantArgs)
			}
		})
	}
}
