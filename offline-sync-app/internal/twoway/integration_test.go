//go:build integration

// Run with: DATABASE_URL=postgres://fieldsync:fieldsync@localhost:5432/fieldsync?sslmode=disable go test -tags=integration ./internal/twoway/...
// Requires `make compose-up` + `make migrate`. Each test uses its own system
// name, so rows never collide and cleanup is a delete by system.
package twoway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"fieldsync/internal/consume"
	"fieldsync/internal/db"
	"fieldsync/internal/mockjira"
	"fieldsync/internal/webhook"
)

const itSecret = "it-secret"

type env struct {
	t       *testing.T
	pool    *pgxpool.Pool
	q       *db.Queries
	system  string
	hookURL string
	jira    *mockjira.Server
	client  *HTTPClient
	sleeps  []time.Duration
	applier *Applier
	worker  *webhook.Worker
	loader  *Loader
	items   *WorkItems
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://fieldsync:fieldsync@localhost:5432/fieldsync?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	e := &env{t: t, pool: pool, q: db.New(pool), system: "it-" + uuid.NewString()[:8]}
	t.Cleanup(e.cleanup)

	verifier := webhook.NewVerifier(itSecret, 5*time.Minute, time.Now)
	receiver := httptest.NewServer(webhook.NewHandler(e.system, verifier, webhook.NewDBInbox(e.q), 1<<20))
	t.Cleanup(receiver.Close)
	e.hookURL = receiver.URL

	e.jira = mockjira.New(mockjira.Options{WebhookURL: receiver.URL, WebhookSecret: itSecret})
	jiraHTTP := httptest.NewServer(e.jira.Handler())
	t.Cleanup(jiraHTTP.Close)

	e.client = NewHTTPClient(jiraHTTP.URL, RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond})
	e.client.sleep = func(_ context.Context, d time.Duration) error {
		e.sleeps = append(e.sleeps, d)
		return nil
	}

	e.applier = NewApplier(e.system, DefaultOwnership())
	e.worker = webhook.NewWorker(e.system, pool, e.applier.ProcessWebhook, 3)
	e.loader = NewLoader(e.system, e.q, e.client, DefaultOwnership())
	e.items = NewWorkItems(NewPgxTransactor(pool))
	return e
}

func (e *env) cleanup() {
	ctx := context.Background()
	linked := "SELECT internal_id FROM external_id_map WHERE system = $1"
	for _, stmt := range []string{
		"DELETE FROM domain_events WHERE aggregate_id IN (" + linked + ")",
		"DELETE FROM work_items WHERE id IN (" + linked + ")",
		"DELETE FROM processed_events WHERE consumer_name = 'twoway-loader-' || $1",
		"DELETE FROM sync_pushed WHERE system = $1",
		"DELETE FROM sync_state WHERE system = $1",
		"DELETE FROM webhook_inbox WHERE system = $1",
		"DELETE FROM sync_dead_letters WHERE system = $1",
		"DELETE FROM external_id_map WHERE system = $1",
	} {
		if _, err := e.pool.Exec(ctx, stmt, e.system); err != nil {
			e.t.Errorf("cleanup %q: %v", stmt, err)
		}
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("query %q: %v", sql, err)
	}
	return n
}

func (e *env) countEvents() int {
	return e.count("SELECT count(*) FROM domain_events WHERE aggregate_id IN (SELECT internal_id FROM external_id_map WHERE system = $1)", e.system)
}

func (e *env) inboxRows() int {
	return e.count("SELECT count(*) FROM webhook_inbox WHERE system = $1", e.system)
}

func (e *env) inboxOutcomes() map[string]string {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), "SELECT delivery_id, outcome FROM webhook_inbox WHERE system = $1", e.system)
	if err != nil {
		e.t.Fatalf("query inbox: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, outcome string
		if err := rows.Scan(&id, &outcome); err != nil {
			e.t.Fatalf("scan: %v", err)
		}
		out[id] = outcome
	}
	return out
}

func (e *env) internalID(externalID string) uuid.UUID {
	e.t.Helper()
	link, err := e.q.GetExternalLink(context.Background(), db.GetExternalLinkParams{System: e.system, ExternalID: externalID})
	if err != nil {
		e.t.Fatalf("link for %s: %v", externalID, err)
	}
	return link.InternalID
}

func (e *env) workItem(externalID string) db.WorkItem {
	e.t.Helper()
	var item db.WorkItem
	item.ID = e.internalID(externalID)
	err := e.pool.QueryRow(context.Background(),
		"SELECT title, status, notes, deleted_at FROM work_items WHERE id = $1", item.ID).
		Scan(&item.Title, &item.Status, &item.Notes, &item.DeletedAt)
	if err != nil {
		e.t.Fatalf("work item for %s: %v", externalID, err)
	}
	return item
}

func (e *env) deliverAndProcess(mode mockjira.FlushMode) {
	e.t.Helper()
	if _, err := e.jira.Flush(mode); err != nil {
		e.t.Fatalf("flush: %v", err)
	}
	if _, err := e.worker.RunOnce(context.Background()); err != nil {
		e.t.Fatalf("worker: %v", err)
	}
}

func (e *env) runLoader() int {
	e.t.Helper()
	n, err := consume.RunOnce(context.Background(), e.q, e.loader.ConsumerName(), e.loader.EventTypes(), e.loader.Handle)
	if err != nil {
		e.t.Fatalf("loader: %v", err)
	}
	return n
}

func (e *env) newExtractor(lister Lister, tx Transactor, pageSize int) *Extractor {
	return NewExtractor(e.system, lister, e.q, tx, e.applier, time.Now,
		ExtractorConfig{Budget: time.Hour, Overlap: 5 * time.Minute, PageSize: pageSize})
}

func TestIntegration_BadSignatureIs401AndNothingIsStored(t *testing.T) {
	e := newEnv(t)
	impostor := mockjira.New(mockjira.Options{WebhookURL: e.hookURL, WebhookSecret: "not-the-shared-secret"})
	impostor.Create("Roof", "Open", "")

	_, err := impostor.Flush(mockjira.FlushInOrder)

	if err == nil || e.inboxRows() != 0 {
		t.Errorf("Flush() error = %v, inbox rows = %d, want a 401 rejection and nothing stored", err, e.inboxRows())
	}
}

func TestIntegration_StaleTimestampIsRejectedEvenWithAValidSignature(t *testing.T) {
	e := newEnv(t)
	e.jira.Create("Roof", "Open", "")
	delivery := e.jira.Pending()[0]

	status, err := e.jira.Deliver(delivery, time.Now().Add(-time.Hour))

	if err != nil || status != http.StatusUnauthorized || e.inboxRows() != 0 {
		t.Errorf("Deliver() = %d, %v with %d inbox rows, want 401 and nothing stored", status, err, e.inboxRows())
	}
	fresh, _ := e.jira.Deliver(delivery, time.Now())
	if fresh != http.StatusAccepted {
		t.Errorf("same delivery with a fresh timestamp = %d, want 202", fresh)
	}
}

func TestIntegration_DuplicateDeliveryIsProcessedOnce(t *testing.T) {
	e := newEnv(t)
	e.jira.Create("Roof", "Open", "")

	e.deliverAndProcess(mockjira.FlushDuplicated)

	if e.inboxRows() != 1 {
		t.Errorf("inbox rows = %d, want 1 for two POSTs of one delivery id", e.inboxRows())
	}
	if e.countEvents() != 1 {
		t.Errorf("domain events = %d, want 1", e.countEvents())
	}
	if n, _ := e.worker.RunOnce(context.Background()); n != 0 {
		t.Errorf("second worker run processed %d, want 0", n)
	}
}

func TestIntegration_OutOfOrderDeliveryIgnoresTheOlderVersion(t *testing.T) {
	e := newEnv(t)
	issue := e.jira.Create("Roof", "Open", "")
	e.jira.Update(issue.ID, mockjira.Patch{Status: strPtr("In Progress")})

	e.deliverAndProcess(mockjira.FlushReversed)

	outcomes := e.inboxOutcomes()
	if outcomes["dlv-2"] != "applied" || outcomes["dlv-1"] != "ignored_stale" {
		t.Errorf("outcomes = %v, want v2 applied and the late v1 ignored_stale", outcomes)
	}
	if got := e.workItem(issue.ID).Status; got != "In Progress" {
		t.Errorf("status = %q, want In Progress: the older event must not overwrite the newer one", got)
	}
}

func TestIntegration_EchoLoopTerminates(t *testing.T) {
	e := newEnv(t)
	issue := e.jira.Create("Roof", "Open", "")
	e.deliverAndProcess(mockjira.FlushInOrder)
	if n := e.runLoader(); n != 1 || e.jira.PatchCalls() != 0 {
		t.Fatalf("loader consumed %d, PATCH calls = %d, want the inbound event consumed without writing back", n, e.jira.PatchCalls())
	}

	if err := e.items.EditNotes(context.Background(), e.internalID(issue.ID), "field notes", "user-1"); err != nil {
		t.Fatalf("EditNotes() error = %v", err)
	}
	e.runLoader()
	if e.jira.PatchCalls() != 1 {
		t.Fatalf("PATCH calls = %d after the local edit, want 1", e.jira.PatchCalls())
	}

	e.deliverAndProcess(mockjira.FlushInOrder)

	if got := e.inboxOutcomes()["dlv-2"]; got != "echo" {
		t.Errorf("webhook for our own write = %q, want echo", got)
	}
	eventsBefore := e.countEvents()
	e.runLoader()
	e.deliverAndProcess(mockjira.FlushInOrder)
	if e.jira.PatchCalls() != 1 || len(e.jira.Pending()) != 0 || e.countEvents() != eventsBefore {
		t.Errorf("PATCH calls = %d, pending webhooks = %d, events %d -> %d, want the loop to be over", e.jira.PatchCalls(), len(e.jira.Pending()), eventsBefore, e.countEvents())
	}
}

func TestIntegration_EchoOfAnOlderPushIsNotReportedAsAConflict(t *testing.T) {
	e := newEnv(t)
	issue := e.jira.Create("Roof", "Open", "")
	e.deliverAndProcess(mockjira.FlushInOrder)
	e.runLoader()
	id := e.internalID(issue.ID)

	e.items.EditNotes(context.Background(), id, "A", "user-1")
	e.runLoader()
	e.items.EditNotes(context.Background(), id, "B", "user-1")
	e.deliverAndProcess(mockjira.FlushInOrder)

	if got := e.inboxOutcomes()["dlv-2"]; got != "echo" {
		t.Errorf("echo of push A after local edit B = %q, want echo, not a false conflict", got)
	}
	e.runLoader()
	e.deliverAndProcess(mockjira.FlushInOrder)

	if got := e.inboxOutcomes()["dlv-3"]; got != "echo" {
		t.Errorf("echo of push B = %q, want echo", got)
	}
	jiraIssue, _ := e.jira.Get(issue.ID)
	if jiraIssue.Description != "B" || e.workItem(issue.ID).Notes != "B" || e.jira.PatchCalls() != 2 {
		t.Errorf("jira=%q internal=%q patches=%d, want both sides at B after exactly 2 pushes", jiraIssue.Description, e.workItem(issue.ID).Notes, e.jira.PatchCalls())
	}
}

func TestIntegration_ConflictKeepsInternalNotesAndAppliesExternalStatus(t *testing.T) {
	e := newEnv(t)
	issue := e.jira.Create("Roof", "Open", "")
	e.deliverAndProcess(mockjira.FlushInOrder)
	e.items.EditNotes(context.Background(), e.internalID(issue.ID), "mine", "user-1")

	e.jira.Update(issue.ID, mockjira.Patch{Status: strPtr("Done"), Description: strPtr("theirs")})
	e.deliverAndProcess(mockjira.FlushInOrder)

	item := e.workItem(issue.ID)
	if e.inboxOutcomes()["dlv-2"] != "conflict" || item.Status != "Done" || item.Notes != "mine" {
		t.Errorf("outcome=%q status=%q notes=%q, want conflict, Done, mine", e.inboxOutcomes()["dlv-2"], item.Status, item.Notes)
	}
}

func TestIntegration_RateLimitedPushHonoursRetryAfter(t *testing.T) {
	e := newEnv(t)
	issue := e.jira.Create("Roof", "Open", "")
	e.deliverAndProcess(mockjira.FlushInOrder)
	e.runLoader()
	e.items.EditNotes(context.Background(), e.internalID(issue.ID), "notes", "user-1")
	e.jira.FailNext(1, http.StatusTooManyRequests, 2*time.Second)

	e.runLoader()

	jiraIssue, _ := e.jira.Get(issue.ID)
	if len(e.sleeps) != 1 || e.sleeps[0] != 2*time.Second || jiraIssue.Description != "notes" || jiraIssue.Version != 2 {
		t.Errorf("sleeps = %v, jira = %+v, want one 2s wait then the write landed once", e.sleeps, jiraIssue)
	}
	if e.count("SELECT count(*) FROM sync_dead_letters WHERE system = $1", e.system) != 0 {
		t.Error("a 429 that recovers must not dead-letter")
	}
}

func TestIntegration_PoisonInboxEventIsDeadLetteredAfterNAttempts(t *testing.T) {
	e := newEnv(t)
	poison := mockjira.Delivery{ID: "poison-1", Body: []byte(`not json`)}
	if status, err := e.jira.Deliver(poison, time.Now()); err != nil || status != http.StatusAccepted {
		t.Fatalf("Deliver() = %d, %v, want 202: a validly signed body is accepted before anyone knows it is poison", status, err)
	}

	for i := 0; i < 5; i++ {
		e.worker.RunOnce(context.Background())
	}

	var source, reason string
	var attempts int32
	var body []byte
	err := e.pool.QueryRow(context.Background(),
		"SELECT source, reason, attempts, payload FROM sync_dead_letters WHERE system = $1 AND ref_id = 'poison-1'", e.system).
		Scan(&source, &reason, &attempts, &body)
	if err != nil {
		t.Fatalf("dead letter lookup: %v", err)
	}
	if source != "inbox" || attempts != 3 || reason == "" || string(body) != "not json" {
		t.Errorf("dead letter = %s/%d/%q/%q, want inbox, exactly 3 attempts, a reason and the raw body", source, attempts, reason, body)
	}
	if e.count("SELECT attempts FROM webhook_inbox WHERE system = $1", e.system) != 3 {
		t.Error("attempts kept growing after the dead letter: that is infinite retry")
	}
}

func TestIntegration_LoaderDeadLettersWhenRetriesAreExhausted(t *testing.T) {
	e := newEnv(t)
	issue := e.jira.Create("Roof", "Open", "")
	e.deliverAndProcess(mockjira.FlushInOrder)
	e.runLoader()
	e.items.EditNotes(context.Background(), e.internalID(issue.ID), "notes", "user-1")
	e.jira.FailNext(100, http.StatusInternalServerError, 0)

	first := e.runLoader()
	second := e.runLoader()

	if first != 1 || second != 0 {
		t.Errorf("loader consumed %d then %d, want the event consumed once and never retried again", first, second)
	}
	if e.count("SELECT count(*) FROM sync_dead_letters WHERE system = $1 AND source = 'loader' AND attempts = 3", e.system) != 1 {
		t.Error("want one loader dead letter recording 3 attempts")
	}
}

func TestIntegration_CrashMidBackfillResumesFromCheckpointWithZeroDuplicates(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 7; i++ {
		e.jira.Create(fmt.Sprintf("Issue %d", i), "Open", "")
	}
	crashing := &crashBeforeCommitTx{pool: e.pool, crashOnCall: 2}
	spy := &spyLister{inner: e.client}

	_, err := e.newExtractor(spy, crashing, 3).Run(context.Background())

	if err == nil {
		t.Fatal("Run() error = nil, want the simulated crash on page 2")
	}
	state, _ := e.q.GetSyncState(context.Background(), e.system)
	if e.count("SELECT count(*) FROM external_id_map WHERE system = $1", e.system) != 3 || state.NextCursor != spy.cursors[1] {
		t.Fatalf("links = %d, checkpoint = %q, want exactly page 1 applied and the checkpoint at page 2's cursor %q",
			e.count("SELECT count(*) FROM external_id_map WHERE system = $1", e.system), state.NextCursor, spy.cursors[1])
	}

	resumed := &spyLister{inner: e.client}
	res, err := e.newExtractor(resumed, NewPgxTransactor(e.pool), 3).Run(context.Background())

	if err != nil || !res.Done {
		t.Fatalf("resumed Run() = %+v, %v, want done", res, err)
	}
	if resumed.cursors[0] != state.NextCursor {
		t.Errorf("resumed from cursor %q, want the checkpoint %q (page 1 must not be fetched again)", resumed.cursors[0], state.NextCursor)
	}
	if e.count("SELECT count(*) FROM external_id_map WHERE system = $1", e.system) != 7 || e.countEvents() != 7 {
		t.Errorf("links = %d, events = %d, want 7 and 7", e.count("SELECT count(*) FROM external_id_map WHERE system = $1", e.system), e.countEvents())
	}
	dupes := e.count(`SELECT count(*) FROM (SELECT aggregate_id FROM domain_events
		WHERE aggregate_id IN (SELECT internal_id FROM external_id_map WHERE system = $1)
		GROUP BY aggregate_id HAVING count(*) > 1) d`, e.system)
	if dupes != 0 {
		t.Errorf("%d work items have more than one event, want zero duplicates", dupes)
	}
	final, _ := e.q.GetSyncState(context.Background(), e.system)
	if final.NextCursor != "" || !final.Watermark.Valid {
		t.Errorf("final state = %+v, want the run closed with a watermark", final)
	}
}

func TestIntegration_IncrementalPullIsIdempotentAcrossTheOverlapWindow(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		e.jira.Create(fmt.Sprintf("Issue %d", i), "Open", "")
	}
	ex := e.newExtractor(e.client, NewPgxTransactor(e.pool), 2)
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if e.countEvents() != 3 {
		t.Fatalf("events after re-reading the overlap = %d, want still 3", e.countEvents())
	}

	e.jira.Update("MJ-2", mockjira.Patch{Status: strPtr("Done")})
	if _, err := ex.Run(context.Background()); err != nil {
		t.Fatalf("third run: %v", err)
	}
	if got := e.workItem("MJ-2").Status; got != "Done" || e.countEvents() != 4 {
		t.Errorf("status = %q, events = %d, want Done and exactly one new event", got, e.countEvents())
	}
}

func TestIntegration_DeletesAreTombstonesAndAMissingPollRowDeletesNothing(t *testing.T) {
	e := newEnv(t)
	e.jira.Create("Keep", "Open", "")
	e.jira.Create("Remove", "Open", "")
	if _, err := e.newExtractor(e.client, NewPgxTransactor(e.pool), 10).Run(context.Background()); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	empty := &spyLister{inner: emptyLister{}}
	if _, err := e.newExtractor(empty, NewPgxTransactor(e.pool), 10).Run(context.Background()); err != nil {
		t.Fatalf("empty poll: %v", err)
	}
	if e.workItem("MJ-1").DeletedAt.Valid || e.workItem("MJ-2").DeletedAt.Valid {
		t.Fatal("a poll that returned no rows must not delete anything")
	}

	e.jira.Delete("MJ-2")
	e.deliverAndProcess(mockjira.FlushInOrder)

	removed, kept := e.workItem("MJ-2"), e.workItem("MJ-1")
	if !removed.DeletedAt.Valid || removed.Title != "Remove" || kept.DeletedAt.Valid {
		t.Errorf("removed=%+v kept=%+v, want a tombstoned row that still exists and an untouched neighbour", removed, kept)
	}
	if e.count("SELECT count(*) FROM work_items WHERE id = $1", removed.ID) != 1 {
		t.Error("the row was hard-deleted")
	}
}

func TestIntegration_DeleteArrivingBeforeCreateStillWinsByVersion(t *testing.T) {
	e := newEnv(t)
	issue := e.jira.Create("Roof", "Open", "")
	e.jira.Delete(issue.ID)

	e.deliverAndProcess(mockjira.FlushReversed)

	outcomes := e.inboxOutcomes()
	if outcomes["dlv-2"] != "applied" || outcomes["dlv-1"] != "ignored_stale" {
		t.Errorf("outcomes = %v, want the delete applied and the late create ignored_stale", outcomes)
	}
	if !e.workItem(issue.ID).DeletedAt.Valid {
		t.Error("work item is not tombstoned: the late create resurrected it")
	}
}

func TestIntegration_ConcurrentFirstSightCreatesOneLinkAndOneItem(t *testing.T) {
	e := newEnv(t)
	issue := ExternalIssue{ID: "MJ-1", Summary: "Roof", Status: "Open", Version: 1}
	tx := NewPgxTransactor(e.pool)

	var wg sync.WaitGroup
	outcomes := make(chan Outcome, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := tx.InTx(context.Background(), func(q db.Querier) error {
				outcome, err := e.applier.Apply(context.Background(), q, SourcePoll, issue)
				outcomes <- outcome
				return err
			})
			if err != nil {
				t.Errorf("Apply() error = %v", err)
			}
		}()
	}
	wg.Wait()
	close(outcomes)

	applied := 0
	for o := range outcomes {
		if o == OutcomeApplied {
			applied++
		}
	}
	if applied != 1 || e.count("SELECT count(*) FROM external_id_map WHERE system = $1", e.system) != 1 || e.countEvents() != 1 {
		t.Errorf("applied = %d, links = %d, events = %d, want exactly one winner and one of each row",
			applied, e.count("SELECT count(*) FROM external_id_map WHERE system = $1", e.system), e.countEvents())
	}
}

type spyLister struct {
	inner   Lister
	cursors []string
}

func (s *spyLister) List(ctx context.Context, since time.Time, cursor string, limit int) (Page, error) {
	s.cursors = append(s.cursors, cursor)
	return s.inner.List(ctx, since, cursor, limit)
}

type emptyLister struct{}

func (emptyLister) List(context.Context, time.Time, string, int) (Page, error) { return Page{}, nil }

type crashBeforeCommitTx struct {
	pool        *pgxpool.Pool
	crashOnCall int
	calls       int
}

func (c *crashBeforeCommitTx) InTx(ctx context.Context, fn func(q db.Querier) error) error {
	c.calls++
	if c.calls != c.crashOnCall {
		return NewPgxTransactor(c.pool).InTx(ctx, fn)
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(db.New(tx)); err != nil {
		return err
	}
	return errors.New("simulated crash after apply, before commit")
}
