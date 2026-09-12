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
	"database/sql"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/thingzio/devtrace/pkg/middleware"
	devnet "github.com/thingzio/devtrace/pkg/net"
	"github.com/thingzio/devtrace/pkg/plan"
	"github.com/thingzio/devtrace/pkg/tenant"
)

type helpData struct {
	Title          string
	Version        string
	Commit         string
	Date           string
	NavUser        string
	NavAvatar      string
	Username       string
	Name           string
	Email          string
	Sent           bool
	Error          string
	ContactEnabled bool
	Plans          []plan.Plan
	Features       []plan.Feature
}

func tryGetTenant(r *http.Request, db *sql.DB) *tenant.Tenant {
	cookie, err := r.Cookie(middleware.SessionCookieName())
	if err != nil {
		return nil
	}
	tn, err := tenant.ValidateSession(r.Context(), db, cookie.Value)
	if err != nil {
		return nil
	}
	return tn
}

func contactEnabled() bool {
	return os.Getenv("SEND_API_KEY") != "" && os.Getenv("SUPPORT_EMAIL") != ""
}

func helpPageHandler(db *sql.DB, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d := helpData{
			Title:          tmplHelp,
			Version:        opts.Version,
			Commit:         opts.Commit,
			Date:           opts.Date,
			ContactEnabled: contactEnabled(),
			Plans:          plan.DisplayPlans(),
			Features:       plan.DisplayFeatures(),
		}
		if tn := tryGetTenant(r, db); tn != nil {
			d.NavUser = tn.Username
			d.NavAvatar = tn.AvatarURL
			d.Username = tn.Username
			d.Name = tn.Name
			d.Email = tn.Email
		}
		renderTemplate(w, "help.html", d)
	}
}

func helpContactHandler(db *sql.DB, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tn := tryGetTenant(r, db)
		if tn == nil || tn.Email == "" {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}

		apiKey := os.Getenv("SEND_API_KEY")
		supportEmail := os.Getenv("SUPPORT_EMAIL")
		if apiKey == "" || supportEmail == "" {
			slog.Error("support email not configured")
			renderHelpWithError(w, r, db, tn, opts, "Contact form is not available at this time.")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		message := strings.TrimSpace(r.FormValue("message"))
		if message == "" {
			renderHelpWithError(w, r, db, tn, opts, "Please enter a message.")
			return
		}

		subject := fmt.Sprintf("DevTrace Support - %s (%s)", tn.Username, tn.Name)
		text := fmt.Sprintf("From: %s (%s)\nEmail: %s\nPlan: %s\n\n%s",
			tn.Username, tn.Name, tn.Email, tn.Plan, message)
		htmlBody := fmt.Sprintf(
			`<p><strong>From:</strong> %s (%s)<br><strong>Email:</strong> %s<br>`+
				`<strong>Plan:</strong> %s</p><hr><p style="white-space:pre-wrap;">%s</p>`,
			html.EscapeString(tn.Username), html.EscapeString(tn.Name),
			html.EscapeString(tn.Email), html.EscapeString(tn.Plan),
			html.EscapeString(message),
		)

		if err := devnet.SendEmail(r.Context(), apiKey, supportEmail, supportEmail, subject, htmlBody, text, tn.Email); err != nil {
			slog.Error("sending support email", "username", tn.Username, "error", err)
			renderHelpWithError(w, r, db, tn, opts, "Failed to send message. Please try again later.")
			return
		}

		slog.Info("support email sent", "from", tn.Email, "username", tn.Username)

		d := helpData{
			Title:          tmplHelp,
			Version:        opts.Version,
			Commit:         opts.Commit,
			Date:           opts.Date,
			NavUser:        tn.Username,
			NavAvatar:      tn.AvatarURL,
			Username:       tn.Username,
			Name:           tn.Name,
			Email:          tn.Email,
			Sent:           true,
			ContactEnabled: true,
			Plans:          plan.DisplayPlans(),
			Features:       plan.DisplayFeatures(),
		}
		renderTemplate(w, "help.html", d)
	}
}

func renderHelpWithError(w http.ResponseWriter, _ *http.Request, _ *sql.DB, tn *tenant.Tenant, opts Options, msg string) {
	d := helpData{
		Title:          tmplHelp,
		Version:        opts.Version,
		Commit:         opts.Commit,
		Date:           opts.Date,
		NavUser:        tn.Username,
		NavAvatar:      tn.AvatarURL,
		Username:       tn.Username,
		Name:           tn.Name,
		Email:          tn.Email,
		Error:          msg,
		ContactEnabled: contactEnabled(),
		Plans:          plan.DisplayPlans(),
		Features:       plan.DisplayFeatures(),
	}
	renderTemplate(w, "help.html", d)
}
