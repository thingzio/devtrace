package server

import (
	"net/http"
)

var errMessages = map[string]string{
	"auth_failed":  "Authentication failed. Please try again.",
	"auth_expired": "Your sign-in session expired. Please try again.",
	"rate_limit":   "Too many requests. Please wait a moment and try again.",
}

type pageData struct {
	Title   string
	Version string
	Commit  string
	Date    string
	Error   string
}

func landingHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var errMsg string
		if code := r.URL.Query().Get("err"); code != "" {
			if msg, ok := errMessages[code]; ok {
				errMsg = msg
			}
		}
		renderTemplate(w, "landing.html", pageData{
			Title:   "Home",
			Version: opts.Version,
			Error:   errMsg,
		})
	}
}
