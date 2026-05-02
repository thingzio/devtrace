package server

// Template data-map keys. Shared across handler files to satisfy goconst
// without forcing each handler to redeclare the same string. These are
// the JSON / HTML template field names — keep stable to avoid breaking
// templates.
const (
	tmplTitle     = "Title"
	tmplVersion   = "Version"
	tmplCommit    = "Commit"
	tmplDate      = "Date"
	tmplAdmin     = "Admin"
	tmplHelp      = "Help"
	tmplName      = "name"
	tmplPlanFree  = "free"
	tmplPlanPro   = "pro"
	tmplErrorKey  = "error"
	tmplNavUser   = "NavUser"
	tmplNavAvatar = "NavAvatar"

	msgRateLimitExceeded = "rate limit exceeded"
)
