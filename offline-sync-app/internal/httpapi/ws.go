package httpapi

import (
	"context"
	"log"
	"net/http"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"fieldsync/internal/auth"
	"fieldsync/internal/db"
	"fieldsync/internal/wsstub"
)

// wsHandler is the local stand-in for a real API Gateway WebSocket API's
// $connect route. A browser's WebSocket API can't set an Authorization
// header, so — matching how a real API Gateway WebSocket JWT authorizer is
// commonly wired — the token travels as a query param instead.
func wsHandler(verifier *auth.Verifier, q db.Querier, hub *wsstub.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		locationID, err := uuid.Parse(r.URL.Query().Get("locationId"))
		if err != nil {
			http.Error(w, "invalid locationId", http.StatusBadRequest)
			return
		}
		claims, err := verifier.VerifyToken(r.Context(), r.URL.Query().Get("token"))
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return // Accept already wrote the error response
		}
		connectionID := uuid.NewString()

		if err := q.InsertWSConnection(r.Context(), db.InsertWSConnectionParams{
			ConnectionID: connectionID,
			UserSub:      claims.Sub,
			LocationID:   locationID,
		}); err != nil {
			log.Printf("ws: insert connection %s: %v", connectionID, err)
			conn.Close(websocket.StatusInternalError, "failed to register connection")
			return
		}
		hub.Register(connectionID, conn)

		defer func() {
			hub.Unregister(connectionID)
			if err := q.DeleteWSConnection(context.Background(), connectionID); err != nil {
				log.Printf("ws: delete connection %s: %v", connectionID, err)
			}
			conn.CloseNow()
		}()

		// Block until the client disconnects — pushes happen from the
		// notifier's goroutine via hub.Push, not from this read loop. Reading
		// is only how Go learns the peer closed the socket.
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
	}
}
