package ticketing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	qrcode "github.com/skip2/go-qrcode"

	"ticketing/internal/db"
	"ticketing/internal/storage"
)

// PresignTTL is short deliberately — docs/plan.md Phase 9's verification
// exercises an EXPIRED presigned URL returning 403 and the client
// refetching, which only means something if the TTL is short enough to
// actually expire inside a test's runtime.
const PresignTTL = 60 * time.Second

var (
	ErrAlreadyRedeemed = errors.New("ticketing: ticket already redeemed or revoked")
	ErrTicketNotFound  = errors.New("ticketing: ticket not found")
)

type Service struct {
	q             db.Querier
	s3            *storage.Client
	signer        Signer
	ticketsBucket string
}

func New(q db.Querier, s3 *storage.Client, signer Signer, ticketsBucket string) *Service {
	return &Service{q: q, s3: s3, signer: signer, ticketsBucket: ticketsBucket}
}

// IssueTicketsForOrder mints one ticket per seat in a TICKETED order,
// renders each as a real QR PNG, and uploads it to the private tickets
// bucket. Called from the order saga's terminal TICKETED transition.
func (s *Service) IssueTicketsForOrder(ctx context.Context, eventID int64, orderID uuid.UUID) ([]db.Ticket, error) {
	seatIDs, err := s.q.ListSeatIDsForOrder(ctx, db.ListSeatIDsForOrderParams{
		EventID: eventID, BookingID: pgtype.UUID{Bytes: orderID, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("list seats for order: %w", err)
	}

	tickets := make([]db.Ticket, 0, len(seatIDs))
	for _, seatID := range seatIDs {
		ticketID := uuid.New()
		key := fmt.Sprintf("tickets/%s.png", ticketID)

		token, err := encodeQR(s.signer, qrPayload{TicketID: ticketID, OrderID: orderID, EventID: eventID, SeatID: seatID})
		if err != nil {
			return nil, fmt.Errorf("encode qr for seat %d: %w", seatID, err)
		}
		png, err := qrcode.Encode(token, qrcode.Medium, 256)
		if err != nil {
			return nil, fmt.Errorf("render qr for seat %d: %w", seatID, err)
		}
		// Private bucket, no cache-control needed — every fetch goes
		// through a fresh short-TTL presigned URL, never a direct/cached
		// public path (docs/plan.md storage module).
		if err := s.s3.PutObject(ctx, s.ticketsBucket, key, png, "image/png", "no-store"); err != nil {
			return nil, fmt.Errorf("upload qr for seat %d: %w", seatID, err)
		}

		ticket, err := s.q.InsertTicket(ctx, db.InsertTicketParams{
			TicketID: ticketID, OrderID: orderID, EventID: eventID, SeatID: seatID, QrS3Key: key,
		})
		if err != nil {
			return nil, fmt.Errorf("insert ticket for seat %d: %w", seatID, err)
		}
		tickets = append(tickets, ticket)
	}
	return tickets, nil
}

// PresignQR returns a short-TTL download URL for a ticket's QR PNG.
func (s *Service) PresignQR(ctx context.Context, ticketID uuid.UUID) (url string, expiresAt time.Time, err error) {
	ticket, err := s.q.GetTicket(ctx, ticketID)
	if err != nil {
		return "", time.Time{}, ErrTicketNotFound
	}
	url, err = s.s3.PresignedGetURL(ctx, s.ticketsBucket, ticket.QrS3Key, PresignTTL)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("presign: %w", err)
	}
	return url, time.Now().Add(PresignTTL), nil
}

// Redeem decodes and verifies a scanned QR token, then atomically flips
// the ticket to redeemed — rowcount 0 from the underlying CAS means
// already-redeemed-or-revoked, not a separate read-then-write race.
func (s *Service) Redeem(ctx context.Context, token string) error {
	payload, err := decodeQR(s.signer, token)
	if err != nil {
		return err // ErrBadSignature or ErrMalformed, both map to 403 at the HTTP layer
	}
	n, err := s.q.RedeemTicket(ctx, payload.TicketID)
	if err != nil {
		return fmt.Errorf("redeem: %w", err)
	}
	if n == 0 {
		return ErrAlreadyRedeemed
	}
	return nil
}
