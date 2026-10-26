package graph

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"fieldsync/internal/db"
)

// Column lists are explicit because pgx.RowToStructByPos matches by position against the sqlc structs.
const (
	locationCols   = "id, name, latitude, longitude, created_at, updated_at, created_by"
	assessmentCols = "id, location_id, name, notes, created_at, updated_at, created_by"
	photoCols      = "id, site_assessment_id, s3_key, latitude, longitude, condition, created_at, updated_at, created_by"
	statsCols      = "location_id, good_count, moderate_count, bad_count, updated_at"
)

type PostgresStore struct {
	conn db.DBTX
}

func NewPostgresStore(conn db.DBTX) *PostgresStore {
	return &PostgresStore{conn: conn}
}

var _ Store = (*PostgresStore)(nil)

func (s *PostgresStore) ListLocations(ctx context.Context, p ListParams) ([]db.DisasterLocation, error) {
	sql, args := listLocationsSQL(p)
	return collect(ctx, s.conn, pgx.RowToStructByPos[db.DisasterLocation], sql, args...)
}

func (s *PostgresStore) LocationsByIDs(ctx context.Context, ids []uuid.UUID) ([]db.DisasterLocation, error) {
	return collect(ctx, s.conn, pgx.RowToStructByPos[db.DisasterLocation],
		"SELECT "+locationCols+" FROM disaster_locations WHERE id = ANY($1)", ids)
}

func (s *PostgresStore) SiteAssessmentsByLocationIDs(ctx context.Context, ids []uuid.UUID) ([]db.SiteAssessment, error) {
	return collect(ctx, s.conn, pgx.RowToStructByPos[db.SiteAssessment],
		"SELECT "+assessmentCols+" FROM site_assessments WHERE location_id = ANY($1) ORDER BY created_at, id", ids)
}

func (s *PostgresStore) PhotosBySiteAssessmentIDs(ctx context.Context, ids []uuid.UUID) ([]db.Photo, error) {
	return collect(ctx, s.conn, pgx.RowToStructByPos[db.Photo],
		"SELECT "+photoCols+" FROM photos WHERE site_assessment_id = ANY($1) ORDER BY created_at, id", ids)
}

func (s *PostgresStore) LocationStatsByLocationIDs(ctx context.Context, ids []uuid.UUID) ([]db.LocationStat, error) {
	return collect(ctx, s.conn, pgx.RowToStructByPos[db.LocationStat],
		"SELECT "+statsCols+" FROM location_stats WHERE location_id = ANY($1)", ids)
}

func collect[T any](ctx context.Context, conn db.DBTX, scan pgx.RowToFunc[T], sql string, args ...any) ([]T, error) {
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scan)
}

// Only placeholders are interpolated into the SQL text; every value travels as a bind argument.
func listLocationsSQL(p ListParams) (string, []any) {
	var (
		conds []string
		args  []any
	)
	if p.UpdatedSince != nil {
		args = append(args, *p.UpdatedSince)
		conds = append(conds, fmt.Sprintf("updated_at >= $%d", len(args)))
	}
	if p.After != nil {
		args = append(args, p.After.UpdatedAt, p.After.ID)
		conds = append(conds, fmt.Sprintf("(updated_at, id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, p.Limit)

	var q strings.Builder
	q.WriteString("SELECT " + locationCols + " FROM disaster_locations")
	if len(conds) > 0 {
		q.WriteString(" WHERE " + strings.Join(conds, " AND "))
	}
	fmt.Fprintf(&q, " ORDER BY updated_at, id LIMIT $%d", len(args))
	return q.String(), args
}
