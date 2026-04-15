# Repo-Scoped Contributor Search UX

## Problem

The search box only accepts a GitHub username. The backend already supports
`?repo=owner/repo` filtering, but there is no way to reach it from the UI.

## Design

### Input Parsing

A single search box accepts two forms:

| Input                          | Parsed username | Parsed repo     |
|--------------------------------|-----------------|-----------------|
| `mchmarny`                     | `mchmarny`      | _(none)_        |
| `mchmarny in nvidia/aicr`     | `mchmarny`      | `nvidia/aicr`   |

Parsing rule: split on the regex `\s+in\s+` (case-sensitive, any amount of
whitespace). The first token is the username, the second (if present) is the
`org/repo` value.

Navigation target:
- Without repo: `/score/{username}`
- With repo: `/score/{username}?repo={org/repo}`

### Placeholder Text

All search inputs (`landing.html`, `home.html`, `scorecard.html`) use:

```
username or username in org/repo
```

### Repo Context in Scorecard Header

When a repo filter is active, display it as a subtitle under the `<h1>` username:

```
mchmarny
in nvidia/aicr        ← secondary text, linked to github.com/nvidia/aicr
```

When no repo filter is present, nothing extra is shown.

### Grade Area Cleanup

Remove `{{.ScoringMode}}` ("global") from the grade display line. The scoring
context is now conveyed by the header subtitle, keeping the grade area compact:
score + version only.

### What stays unchanged

- Backend handler (`handler_score.go`) — already accepts `?repo=owner/repo`
- Scorecard page handler (`handler_pages.go`) — already passes `repo` to service
- `RepoContext` section at bottom of scorecard — still renders when data exists

## Files to Change

| File | Change |
|------|--------|
| `pkg/server/static/js/app.js` | Parse `username in org/repo`, build URL with `?repo=` |
| `pkg/server/templates/landing.html` | Update placeholder text |
| `pkg/server/templates/home.html` | Update placeholder text |
| `pkg/server/templates/scorecard.html` | Update placeholder, add repo subtitle in header, remove ScoringMode from grade |
