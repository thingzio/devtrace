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

package net

import (
	"net/http"
	"time"
)

// GitHubClient is a shared HTTP client for GitHub API and OAuth calls.
// Does not follow redirects (OAuth token exchange needs raw response).
var GitHubClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// QuotaCheckClient is a lightweight HTTP client for GitHub rate limit checks.
var QuotaCheckClient = &http.Client{Timeout: 5 * time.Second}
