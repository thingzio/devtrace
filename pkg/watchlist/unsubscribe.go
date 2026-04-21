package watchlist

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
)

// HMACSecret returns the DIGEST_HMAC_SECRET env var used for unsubscribe tokens.
func HMACSecret() string {
	return os.Getenv("DIGEST_HMAC_SECRET")
}

// UnsubscribeToken generates an HMAC-SHA256 token for one-click unsubscribe.
func UnsubscribeToken(secret, tenantID string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tenantID))
	return hex.EncodeToString(mac.Sum(nil))
}

// ValidateUnsubscribeToken checks an unsubscribe token using constant-time comparison.
func ValidateUnsubscribeToken(secret, tenantID, token string) bool {
	expected := UnsubscribeToken(secret, tenantID)
	return hmac.Equal([]byte(expected), []byte(token))
}
