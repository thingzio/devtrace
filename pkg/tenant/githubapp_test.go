package tenant_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/thingzio/devtrace/pkg/tenant"
)

func testAppConfig(t *testing.T) (*tenant.GitHubAppConfig, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return &tenant.GitHubAppConfig{
		AppID:      12345,
		PrivateKey: key,
	}, key
}

func TestCreateAppJWT(t *testing.T) {
	cfg, key := testAppConfig(t)

	signed, err := tenant.CreateAppJWT(cfg)
	if err != nil {
		t.Fatalf("CreateAppJWT: %v", err)
	}

	token, err := jwt.ParseWithClaims(signed, &jwt.RegisteredClaims{}, func(_ *jwt.Token) (any, error) {
		return &key.PublicKey, nil
	})
	if err != nil {
		t.Fatalf("parse JWT: %v", err)
	}

	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok {
		t.Fatal("unexpected claims type")
	}

	if claims.Issuer != "12345" {
		t.Errorf("issuer = %q, want %q", claims.Issuer, "12345")
	}

	now := time.Now()

	// IssuedAt should be ~60s in the past (clock drift tolerance).
	iat := claims.IssuedAt.Time
	if diff := now.Sub(iat); diff < 50*time.Second || diff > 70*time.Second {
		t.Errorf("iat drift = %v, expected ~60s", diff)
	}

	// ExpiresAt should be ~10 minutes from now.
	exp := claims.ExpiresAt.Time
	if diff := exp.Sub(now); diff < 9*time.Minute || diff > 11*time.Minute {
		t.Errorf("exp diff = %v, expected ~10m", diff)
	}
}

func TestMintInstallationToken(t *testing.T) {
	cfg, _ := testAppConfig(t)

	wantExpiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/app/installations/42/access_tokens" {
			t.Errorf("path = %s, unexpected", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth == "" {
			t.Error("missing Authorization header")
		}

		w.WriteHeader(http.StatusCreated)
		resp := map[string]any{
			"token":      "v1.test-token",
			"expires_at": wantExpiry.Format(time.RFC3339),
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	cfg.BaseURL = srv.URL

	tok, err := tenant.MintInstallationToken(context.Background(), cfg, 42)
	if err != nil {
		t.Fatalf("MintInstallationToken: %v", err)
	}
	if tok.Token != "v1.test-token" {
		t.Errorf("token = %q, want %q", tok.Token, "v1.test-token")
	}
	if !tok.ExpiresAt.Equal(wantExpiry) {
		t.Errorf("expires_at = %v, want %v", tok.ExpiresAt, wantExpiry)
	}
}
