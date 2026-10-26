package graph

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"

	"fieldsync/internal/auth"
)

const (
	authKid      = "test-key"
	authAudience = "test-client-id"
)

func newJWKSIssuer(t *testing.T) (issuer string, priv *rsa.PrivateKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub, err := jwk.Import(priv.PublicKey)
	if err != nil {
		t.Fatalf("import public key: %v", err)
	}
	if err := pub.Set("kid", authKid); err != nil {
		t.Fatalf("set kid: %v", err)
	}
	if err := pub.Set("alg", jwa.RS256()); err != nil {
		t.Fatalf("set alg: %v", err)
	}
	set := jwk.NewSet()
	if err := set.AddKey(pub); err != nil {
		t.Fatalf("add key: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(set)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, priv
}

func signIDToken(t *testing.T, priv *rsa.PrivateKey, issuer string, exp time.Time) string {
	t.Helper()
	tok, err := jwt.NewBuilder().
		Issuer(issuer).
		Audience([]string{authAudience}).
		Subject("user-123").
		IssuedAt(time.Now()).
		Expiration(exp).
		Claim("token_use", "id").
		Build()
	if err != nil {
		t.Fatalf("build token: %v", err)
	}
	key, err := jwk.Import(priv)
	if err != nil {
		t.Fatalf("import private key: %v", err)
	}
	if err := key.Set("kid", authKid); err != nil {
		t.Fatalf("set kid: %v", err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), key))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return string(signed)
}

func doAuthed(h http.Handler, authorization string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"query": `{ locations(first: 1) { edges { cursor } } }`})
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuth_UnauthenticatedRequestIsRejectedBeforeAnyStoreCall(t *testing.T) {
	issuer, _ := newJWKSIssuer(t)
	store := newFakeStore(3, 1, 1)
	h := NewHandler(store, auth.NewVerifier(issuer, authAudience).Middleware, testConfig())

	rec := doAuthed(h, "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if store.totalCalls() != 0 {
		t.Fatalf("store calls = %d, want 0", store.totalCalls())
	}
}

func TestAuth_GarbageAndExpiredTokensAreRejected(t *testing.T) {
	issuer, priv := newJWKSIssuer(t)
	store := newFakeStore(3, 1, 1)
	h := NewHandler(store, auth.NewVerifier(issuer, authAudience).Middleware, testConfig())

	for name, header := range map[string]string{
		"garbage bearer": "Bearer not.a.jwt",
		"wrong scheme":   "Basic dXNlcjpwYXNz",
		"expired":        "Bearer " + signIDToken(t, priv, issuer, time.Now().Add(-time.Hour)),
	} {
		if rec := doAuthed(h, header); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rec.Code)
		}
	}
	if store.totalCalls() != 0 {
		t.Fatalf("store calls = %d, want 0", store.totalCalls())
	}
}

func TestAuth_ValidTokenReachesResolvers(t *testing.T) {
	issuer, priv := newJWKSIssuer(t)
	store := newFakeStore(3, 1, 1)
	h := NewHandler(store, auth.NewVerifier(issuer, authAudience).Middleware, testConfig())

	rec := doAuthed(h, "Bearer "+signIDToken(t, priv, issuer, time.Now().Add(time.Hour)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body)
	}
	if got := store.callCount("ListLocations"); got != 1 {
		t.Fatalf("ListLocations calls = %d, want 1", got)
	}
}
