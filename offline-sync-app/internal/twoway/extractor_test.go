package twoway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"fieldsync/internal/db"
	"fieldsync/internal/sync/mocks"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type listCall struct {
	since  time.Time
	cursor string
}

type fakeLister struct {
	clock    *fakeClock
	pageCost time.Duration
	pages    map[string]Page
	failOn   string
	calls    []listCall
}

func (l *fakeLister) List(_ context.Context, since time.Time, cursor string, _ int) (Page, error) {
	l.calls = append(l.calls, listCall{since: since, cursor: cursor})
	if cursor == l.failOn && l.failOn != "!" {
		return Page{}, errors.New("connection reset")
	}
	l.clock.now = l.clock.now.Add(l.pageCost)
	return l.pages[cursor], nil
}

type recordingApplier struct {
	applied []string
	failOn  string
}

func (a *recordingApplier) Apply(_ context.Context, _ db.Querier, _ Source, issue ExternalIssue) (Outcome, error) {
	if issue.ID == a.failOn {
		return "", errors.New("db error")
	}
	a.applied = append(a.applied, issue.ID)
	return OutcomeApplied, nil
}

func ts(secs int64) time.Time { return time.Unix(secs, 0).UTC() }

func tsz(secs int64) pgtype.Timestamptz { return pgtype.Timestamptz{Time: ts(secs), Valid: true} }

var zeroSince = pgtype.Timestamptz{Time: time.Time{}, Valid: true}

func threePages() map[string]Page {
	return map[string]Page{
		"":   {Issues: []ExternalIssue{{ID: "A", UpdatedAt: ts(100)}, {ID: "B", UpdatedAt: ts(110)}}, NextCursor: "c1"},
		"c1": {Issues: []ExternalIssue{{ID: "C", UpdatedAt: ts(120)}, {ID: "D", UpdatedAt: ts(130)}}, NextCursor: "c2"},
		"c2": {Issues: []ExternalIssue{{ID: "E", UpdatedAt: ts(140)}}},
	}
}

type extractorFixture struct {
	extractor *Extractor
	q         *mocks.MockQuerier
	lister    *fakeLister
	applier   *recordingApplier
	clock     *fakeClock
}

func newExtractorFixture(t *testing.T, budget time.Duration, pageCost time.Duration) *extractorFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &extractorFixture{
		q:       mocks.NewMockQuerier(ctrl),
		clock:   &fakeClock{now: ts(1_000_000)},
		applier: &recordingApplier{},
	}
	f.lister = &fakeLister{clock: f.clock, pageCost: pageCost, pages: threePages(), failOn: "!"}
	f.extractor = NewExtractor(testSystem, f.lister, f.q, passthroughTx{q: f.q}, f.applier, f.clock.Now,
		ExtractorConfig{Budget: budget, Overlap: 5 * time.Minute, PageSize: 2})
	return f
}

func TestRun_FirstRunBackfillsEverythingAndSetsWatermark(t *testing.T) {
	f := newExtractorFixture(t, time.Hour, time.Second)
	f.q.EXPECT().GetSyncState(gomock.Any(), testSystem).Return(db.SyncState{}, pgx.ErrNoRows)
	gomock.InOrder(
		f.q.EXPECT().UpsertSyncState(gomock.Any(), db.UpsertSyncStateParams{System: testSystem, RunSince: zeroSince, NextCursor: "c1", RunMax: tsz(110)}).Return(nil),
		f.q.EXPECT().UpsertSyncState(gomock.Any(), db.UpsertSyncStateParams{System: testSystem, RunSince: zeroSince, NextCursor: "c2", RunMax: tsz(130)}).Return(nil),
		f.q.EXPECT().UpsertSyncState(gomock.Any(), db.UpsertSyncStateParams{System: testSystem, Watermark: tsz(140)}).Return(nil),
	)

	res, err := f.extractor.Run(context.Background())

	if err != nil || !res.Done || res.Pages != 3 || res.Issues != 5 {
		t.Errorf("Run() = %+v, %v, want 3 pages / 5 issues, done", res, err)
	}
	if !f.lister.calls[0].since.IsZero() {
		t.Errorf("first run since = %v, want zero (full backfill)", f.lister.calls[0].since)
	}
}

func TestRun_IncrementalRunStartsOverlapBeforeTheWatermark(t *testing.T) {
	f := newExtractorFixture(t, time.Hour, time.Second)
	f.q.EXPECT().GetSyncState(gomock.Any(), testSystem).Return(db.SyncState{Watermark: tsz(10_000)}, nil)
	f.q.EXPECT().UpsertSyncState(gomock.Any(), gomock.Any()).Return(nil).Times(3)

	if _, err := f.extractor.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if want := ts(10_000).Add(-5 * time.Minute); !f.lister.calls[0].since.Equal(want) {
		t.Errorf("since = %v, want watermark - overlap = %v", f.lister.calls[0].since, want)
	}
}

func TestRun_WatermarkNeverMovesBackwardsWhenTheRunSeesOlderRows(t *testing.T) {
	f := newExtractorFixture(t, time.Hour, time.Second)
	f.q.EXPECT().GetSyncState(gomock.Any(), testSystem).Return(db.SyncState{Watermark: tsz(100_000)}, nil)
	f.q.EXPECT().UpsertSyncState(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	f.q.EXPECT().UpsertSyncState(gomock.Any(), db.UpsertSyncStateParams{System: testSystem, Watermark: tsz(100_000)}).Return(nil)

	if _, err := f.extractor.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRun_StopsBeforeAPageItCannotFinishAndResumesFromTheCheckpoint(t *testing.T) {
	f := newExtractorFixture(t, 100*time.Second, 40*time.Second)
	f.q.EXPECT().GetSyncState(gomock.Any(), testSystem).Return(db.SyncState{}, pgx.ErrNoRows)
	f.q.EXPECT().UpsertSyncState(gomock.Any(), gomock.Any()).Return(nil).Times(2)

	first, err := f.extractor.Run(context.Background())

	if err != nil || first.Done || first.Pages != 2 {
		t.Fatalf("first Run() = %+v, %v, want 2 pages and not done: 100s budget, 40s pages, 20s left < 40s slowest", first, err)
	}
	if len(f.lister.calls) != 2 {
		t.Errorf("List called %d times, want 2: the third page must not even be requested", len(f.lister.calls))
	}

	f.lister.calls = nil
	f.applier.applied = nil
	f.clock.now = f.clock.now.Add(time.Hour)
	f.q.EXPECT().GetSyncState(gomock.Any(), testSystem).Return(db.SyncState{RunSince: tsz(7), NextCursor: "c2", RunMax: tsz(130)}, nil)
	f.q.EXPECT().UpsertSyncState(gomock.Any(), db.UpsertSyncStateParams{System: testSystem, Watermark: tsz(140)}).Return(nil)

	second, err := f.extractor.Run(context.Background())

	if err != nil || !second.Done || second.Pages != 1 {
		t.Fatalf("second Run() = %+v, %v, want the last page and done", second, err)
	}
	if got := f.lister.calls[0]; got.cursor != "c2" || !got.since.Equal(ts(7)) {
		t.Errorf("resumed with %+v, want cursor c2 and the stored run_since", got)
	}
	if len(f.applier.applied) != 1 || f.applier.applied[0] != "E" {
		t.Errorf("applied %v after resume, want only E: pages before the checkpoint are never re-applied", f.applier.applied)
	}
}

func TestRun_ZeroBudgetDoesNotStartAPage(t *testing.T) {
	f := newExtractorFixture(t, 0, time.Second)
	f.q.EXPECT().GetSyncState(gomock.Any(), testSystem).Return(db.SyncState{}, pgx.ErrNoRows)

	res, err := f.extractor.Run(context.Background())

	if err != nil || res.Done || len(f.lister.calls) != 0 {
		t.Errorf("Run() = %+v, %v with %d List calls, want a clean exit before any page", res, err, len(f.lister.calls))
	}
}

func TestRun_CrashOnSecondPageKeepsTheFirstCheckpoint(t *testing.T) {
	f := newExtractorFixture(t, time.Hour, time.Second)
	f.lister.failOn = "c1"
	f.q.EXPECT().GetSyncState(gomock.Any(), testSystem).Return(db.SyncState{}, pgx.ErrNoRows)
	f.q.EXPECT().UpsertSyncState(gomock.Any(), db.UpsertSyncStateParams{System: testSystem, RunSince: zeroSince, NextCursor: "c1", RunMax: tsz(110)}).Return(nil)

	res, err := f.extractor.Run(context.Background())

	if err == nil || res.Pages != 1 {
		t.Errorf("Run() = %+v, %v, want 1 committed page and an error", res, err)
	}
}

func TestRun_FailedApplyDoesNotAdvanceTheCheckpoint(t *testing.T) {
	f := newExtractorFixture(t, time.Hour, time.Second)
	f.applier.failOn = "B"
	f.q.EXPECT().GetSyncState(gomock.Any(), testSystem).Return(db.SyncState{}, pgx.ErrNoRows)

	_, err := f.extractor.Run(context.Background())

	if err == nil {
		t.Error("Run() error = nil, want the apply failure; no UpsertSyncState is expected, so the page replays next run")
	}
}
