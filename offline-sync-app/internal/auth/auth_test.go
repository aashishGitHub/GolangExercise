package auth

import (
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
)

const testKid = "test-key"

// newTestIssuer starts an httptest server serving a JWKS at
// /.well-known/jwks.json, standing in for cognito-local, and returns the
// issuer URL plus the private key to sign tokens with.
func newTestIssuer(t *testing.T) (issuer string, priv *rsa.PrivateKey) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	pub, err := jwk.Import(priv.PublicKey)
	if err != nil {
		t.Fatalf("import public key: %v", err)
	}
	if err := pub.Set("kid", testKid); err != nil {
		t.Fatalf("set kid: %v", err)
	}
	if err := pub.Set("alg", jwa.RS256()); err != nil {
		t.Fatalf("set alg: %v", err)
	}

	set := jwk.NewSet()
	if err := set.AddKey(pub); err != nil {
		t.Fatalf("add key to set: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(set)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv.URL, priv
}

func signToken(t *testing.T, priv *rsa.PrivateKey, issuer, audience, tokenUse string, claims map[string]any, exp time.Time) string {
	t.Helper()

	b := jwt.NewBuilder().
		Issuer(issuer).
		Audience([]string{audience}).
		Subject("user-123").
		IssuedAt(time.Now()).
		Expiration(exp).
		Claim("token_use", tokenUse)
	for k, v := range claims {
		b = b.Claim(k, v)
	}
	tok, err := b.Build()
	if err != nil {
		t.Fatalf("build token: %v", err)
	}

	key, err := jwk.Import(priv)
	if err != nil {
		t.Fatalf("import private key: %v", err)
	}
	if err := key.Set("kid", testKid); err != nil {
		t.Fatalf("set kid on private key: %v", err)
	}

	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), key))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return string(signed)
}

func doRequest(t *testing.T, v *Verifier, authHeader string) (status int, claims Claims, hasClaims bool) {
	t.Helper()

	var got Claims
	var ok bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	v.Middleware(next).ServeHTTP(rec, req)
	return rec.Code, got, ok
}

func TestMiddleware_ValidToken(t *testing.T) {
	issuer, priv := newTestIssuer(t)
	audience := "test-client-id"
	v := NewVerifier(issuer, audience)

	token := signToken(t, priv, issuer, audience, "id",
		map[string]any{"email": "field-worker@example.com"}, time.Now().Add(time.Hour))

	status, claims, ok := doRequest(t, v, "Bearer "+token)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !ok {
		t.Fatal("expected claims in context")
	}
	if claims.Sub != "user-123" {
		t.Errorf("Sub = %q, want %q", claims.Sub, "user-123")
	}
	if claims.Email != "field-worker@example.com" {
		t.Errorf("Email = %q, want %q", claims.Email, "field-worker@example.com")
	}
}

func TestMiddleware_MissingToken(t *testing.T) {
	issuer, _ := newTestIssuer(t)
	v := NewVerifier(issuer, "test-client-id")

	status, _, ok := doRequest(t, v, "")

	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
	if ok {
		t.Fatal("expected no claims in context")
	}
}

func TestMiddleware_ExpiredToken(t *testing.T) {
	issuer, priv := newTestIssuer(t)
	audience := "test-client-id"
	v := NewVerifier(issuer, audience)

	token := signToken(t, priv, issuer, audience, "id", nil, time.Now().Add(-time.Hour))

	status, _, _ := doRequest(t, v, "Bearer "+token)
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for expired token", status)
	}
}

func TestMiddleware_WrongAudience(t *testing.T) {
	issuer, priv := newTestIssuer(t)
	v := NewVerifier(issuer, "expected-client-id")

	token := signToken(t, priv, issuer, "some-other-client-id", "id", nil, time.Now().Add(time.Hour))

	status, _, _ := doRequest(t, v, "Bearer "+token)
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for wrong audience", status)
	}
}

func TestMiddleware_WrongTokenUse(t *testing.T) {
	issuer, priv := newTestIssuer(t)
	audience := "test-client-id"
	v := NewVerifier(issuer, audience)

	// access tokens don't carry email/attribution claims the way ID tokens
	// do — this middleware only accepts token_use=id.
	token := signToken(t, priv, issuer, audience, "access", nil, time.Now().Add(time.Hour))

	status, _, _ := doRequest(t, v, "Bearer "+token)
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for token_use=access", status)
	}
}
