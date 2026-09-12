// Copyright 2026 Thingz LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

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
