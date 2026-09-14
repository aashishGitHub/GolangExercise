// Package auth verifies Cognito-issued JWTs the same way an API Gateway JWT
// authorizer would: fetch the user pool's JWKS, verify the RS256 signature,
// check issuer/audience/token_use, then hand sub/email to the handler via
// context — mirroring "Lambda reads sub/email from the authorizer claims" in
// the plan doc, so this middleware becomes a no-op once API Gateway does the
// verification in prod (Phase 5).
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

type Claims struct {
	Sub   string
	Email string
}

type ctxKey struct{}

// Verifier holds one issuer's JWKS with a simple TTL cache. Good enough for
// local dev against cognito-local; API Gateway does this in real AWS.
type Verifier struct {
	issuer   string
	audience string
	jwksURL  string
	ttl      time.Duration

	mu        sync.Mutex
	set       jwk.Set
	fetchedAt time.Time
}

func NewVerifier(issuer, audience string) *Verifier {
	return &Verifier{
		issuer:   issuer,
		audience: audience,
		jwksURL:  strings.TrimRight(issuer, "/") + "/.well-known/jwks.json",
		ttl:      5 * time.Minute,
	}
}

func (v *Verifier) keySet(ctx context.Context) (jwk.Set, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.set != nil && time.Since(v.fetchedAt) < v.ttl {
		return v.set, nil
	}

	set, err := jwk.Fetch(ctx, v.jwksURL)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks from %s: %w", v.jwksURL, err)
	}
	v.set, v.fetchedAt = set, time.Now()
	return set, nil
}

var (
	ErrMissingToken  = errors.New("missing bearer token")
	ErrInvalidClaims = errors.New("token failed claim checks")
)

// Middleware gates the routes it wraps behind a valid Cognito ID token —
// "auth gates sync, not capture": mount this only on sync/API routes, never
// on capture/offline-write paths.
func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := v.verify(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (v *Verifier) verify(r *http.Request) (Claims, error) {
	authHeader := r.Header.Get("Authorization")
	tokenStr, ok := strings.CutPrefix(authHeader, "Bearer ")
	if !ok || tokenStr == "" {
		return Claims{}, ErrMissingToken
	}
	return v.VerifyToken(r.Context(), tokenStr)
}

// VerifyToken is the same check as Middleware, for callers that don't have
// an Authorization header to work with — the WebSocket stub's $connect
// equivalent authenticates via a query param instead (a browser's WebSocket
// API can't set custom headers), mirroring how a real API Gateway WebSocket
// JWT-in-query-string authorizer would be wired.
func (v *Verifier) VerifyToken(ctx context.Context, tokenStr string) (Claims, error) {
	if tokenStr == "" {
		return Claims{}, ErrMissingToken
	}

	set, err := v.keySet(ctx)
	if err != nil {
		return Claims{}, err
	}

	tok, err := jwt.Parse([]byte(tokenStr),
		jwt.WithKeySet(set),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidClaims, err)
	}

	var tokenUse string
	if err := tok.Get("token_use", &tokenUse); err != nil || tokenUse != "id" {
		return Claims{}, fmt.Errorf("%w: token_use must be id", ErrInvalidClaims)
	}

	sub, _ := tok.Subject()
	var email string
	_ = tok.Get("email", &email)

	return Claims{Sub: sub, Email: email}, nil
}

func FromContext(ctx context.Context) (Claims, bool) {
	claims, ok := ctx.Value(ctxKey{}).(Claims)
	return claims, ok
}
