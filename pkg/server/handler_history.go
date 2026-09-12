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

package server

import (
	"net/http"

	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/middleware"
	"github.com/thingzio/devtrace/pkg/plan"
)

// historyDays returns the score history window based on the tenant's plan.
func historyDays(planName string) int {
	if p, ok := plan.Get(planName); ok {
		return p.HistoryDays
	}
	return plan.Free().HistoryDays
}

func historyHandler(store *postgres.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		if username == "" || len(username) > 39 || !usernameRE.MatchString(username) {
			writeError(w, http.StatusBadRequest, "invalid username")
			return
		}

		planName := ""
		if tn := middleware.TenantFromContext(r.Context()); tn != nil {
			planName = tn.Plan
		}

		entries, err := store.GetScoreHistory(r.Context(), username, "github", historyDays(planName))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to fetch history")
			return
		}
		if entries == nil {
			entries = []postgres.ScoreHistoryEntry{}
		}
		writeJSON(w, http.StatusOK, entries)
	}
}
