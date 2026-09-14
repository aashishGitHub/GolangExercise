package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"ticketing/internal/db"
	"ticketing/internal/inventory/mocks"
)

// fakeLocker is a no-op Locker for unit tests that exercise ReleaseHold's
// (best-effort, non-fatal) Redis call without dialing real Redis.
type fakeLocker struct{}

func (fakeLocker) Acquire(context.Context, int64, int64, string, time.Duration) (bool, error) {
	return true, nil
}
func (fakeLocker) Release(context.Context, int64, int64, string) error { return nil }

// newTestService builds a Service against a mocked Querier and a no-op
// Locker. AcquireHold's validation-only early returns (duplicate/too-many/
// empty seat lists) never touch pool or lock, so a nil pool is safe too —
// anything that DOES need a real transaction (the CAS itself) is
// integration-tested in integration_test.go against real Postgres + Redis,
// not mocked here.
func newTestService(t *testing.T) (*Service, *mocks.MockQuerier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := mocks.NewMockQuerier(ctrl)
	return New(nil, q, fakeLocker{}, 10*time.Minute, 8), q
}

func TestAcquireHold_DuplicateSeatRejectedBeforeAnyIO(t *testing.T) {
	svc, q := newTestService(t)
	// No mock expectations set — if AcquireHold reached the DB or Redis for
	// this input, the test fails on an unexpected call.
	_ = q

	_, err := svc.AcquireHold(context.Background(), 1, []int64{5, 3, 5}, "user-1")
	if !errors.Is(err, ErrDuplicateSeat) {
		t.Fatalf("err = %v, want ErrDuplicateSeat", err)
	}
}

func TestAcquireHold_TooManySeatsRejectedBeforeAnyIO(t *testing.T) {
	svc, _ := newTestService(t)
	seats := make([]int64, 9) // maxSeatsPerHold is 8 in newTestService
	for i := range seats {
		seats[i] = int64(i + 1)
	}

	_, err := svc.AcquireHold(context.Background(), 1, seats, "user-1")
	if !errors.Is(err, ErrTooManySeats) {
		t.Fatalf("err = %v, want ErrTooManySeats", err)
	}
}

func TestAcquireHold_EmptySeatListRejected(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.AcquireHold(context.Background(), 1, nil, "user-1")
	if err == nil {
		t.Fatal("expected an error for an empty seat list")
	}
}

func TestReleaseHold_RowcountZeroIsSuccessNotError(t *testing.T) {
	svc, q := newTestService(t)
	holdID := uuid.New()

	// Simulate "already gone" — the DB matches zero rows. ReleaseHold must
	// still return nil, not an error (docs/plan.md: idempotent by construction).
	q.EXPECT().
		ReleaseHold(gomock.Any(), gomock.Any()).
		Return(int64(0), nil)
	q.EXPECT().
		InsertHoldsAudit(gomock.Any(), gomock.Any()).
		Return(nil)

	if err := svc.ReleaseHold(context.Background(), 1, []int64{5}, holdID, "user-1"); err != nil {
		t.Fatalf("ReleaseHold with rowcount 0: err = %v, want nil", err)
	}
}

func TestReleaseHold_PropagatesRealDBErrors(t *testing.T) {
	svc, q := newTestService(t)
	wantErr := errors.New("connection reset")

	q.EXPECT().
		ReleaseHold(gomock.Any(), gomock.Any()).
		Return(int64(0), wantErr)

	err := svc.ReleaseHold(context.Background(), 1, []int64{5}, uuid.New(), "user-1")
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wrapping %v", err, wantErr)
	}
}

func TestExtendHold_ShortRowcountReturnsErrHoldExpired(t *testing.T) {
	svc, q := newTestService(t)

	// Asked to extend 2 seats, only 1 still matched (status IN (1,3) AND
	// hold_expires_at > now()) — the hold already lapsed on the other one.
	q.EXPECT().
		ExtendHold(gomock.Any(), gomock.Any()).
		Return(int64(1), nil)

	n, err := svc.ExtendHold(context.Background(), 1, []int64{5, 6}, uuid.New(), time.Minute)
	if n != 1 {
		t.Errorf("n = %d, want 1", n)
	}
	if !errors.Is(err, ErrHoldExpired) {
		t.Fatalf("err = %v, want ErrHoldExpired", err)
	}
}

func TestExtendHold_FullRowcountIsSuccess(t *testing.T) {
	svc, q := newTestService(t)

	q.EXPECT().
		ExtendHold(gomock.Any(), gomock.Any()).
		Return(int64(2), nil)

	n, err := svc.ExtendHold(context.Background(), 1, []int64{5, 6}, uuid.New(), time.Minute)
	if err != nil {
		t.Fatalf("unexpected err = %v", err)
	}
	if n != 2 {
		t.Errorf("n = %d, want 2", n)
	}
}

func TestConfirmSeats_MismatchedLengthsRejectedBeforeAnyIO(t *testing.T) {
	svc, _ := newTestService(t)
	err := svc.ConfirmSeats(context.Background(), 1, []int64{5, 6}, []int64{1}, uuid.New(), uuid.New())
	if err == nil {
		t.Fatal("expected an error for mismatched seatIDs/fences lengths")
	}
}

func TestSortDedup(t *testing.T) {
	cases := []struct {
		in       []int64
		wantOut  []int64
		wantDupe bool
	}{
		{[]int64{3, 1, 2}, []int64{1, 2, 3}, false},
		{[]int64{5, 5}, []int64{5}, true},
		{[]int64{1}, []int64{1}, false},
		{[]int64{9, 1, 9, 5}, []int64{1, 5, 9}, true},
	}
	for _, c := range cases {
		got, hadDupes := sortDedup(c.in)
		if hadDupes != c.wantDupe {
			t.Errorf("sortDedup(%v) hadDupes = %v, want %v", c.in, hadDupes, c.wantDupe)
		}
		if len(got) != len(c.wantOut) {
			t.Fatalf("sortDedup(%v) = %v, want %v", c.in, got, c.wantOut)
		}
		for i := range got {
			if got[i] != c.wantOut[i] {
				t.Errorf("sortDedup(%v) = %v, want %v", c.in, got, c.wantOut)
			}
		}
	}
}

func TestSortSeatsAndFencesTogether(t *testing.T) {
	seats := []int64{30, 10, 20}
	fences := []int64{3, 1, 2} // fences[i] belongs to seats[i] before sorting
	sortSeatsAndFencesTogether(seats, fences)

	wantSeats := []int64{10, 20, 30}
	wantFences := []int64{1, 2, 3} // must move WITH their seat, not independently
	for i := range seats {
		if seats[i] != wantSeats[i] || fences[i] != wantFences[i] {
			t.Fatalf("got seats=%v fences=%v, want seats=%v fences=%v", seats, fences, wantSeats, wantFences)
		}
	}
}

var _ db.Querier = (*mocks.MockQuerier)(nil) // compile-time: the mock satisfies the real interface
