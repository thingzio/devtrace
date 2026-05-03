package server

// Template data-map keys. Shared across handler files to satisfy goconst
// without forcing each handler to redeclare the same string. These are
// the JSON / HTML template field names — keep stable to avoid breaking
// templates.
const (
	tmplTitle        = "Title"
	tmplVersion      = "Version"
	tmplCommit       = "Commit"
	tmplDate         = "Date"
	tmplAdmin        = "Admin"
	tmplHelp         = "Help"
	tmplName         = "name"
	tmplPlanFree     = "free"
	tmplPlanPro      = "pro"
	tmplErrorKey     = "error"
	tmplNavUser      = "NavUser"
	tmplNavAvatar    = "NavAvatar"
	tmplUsername     = "Username"
	tmplGrade        = "Grade"
	tmplGradeClass   = "GradeClass"
	tmplBio          = "bio"
	tmplModelVersion = "ModelVersion"
	tmplValue        = "Value"

	msgRateLimitExceeded = "rate limit exceeded"

	// authGitHubPath is the OAuth start URL. Hoisted because handlers,
	// middleware, and ratelimit JSON envelopes all need to reference it.
	authGitHubPath = "/auth/github"
)
