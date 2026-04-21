# Watchlist & Email Digest — Pre-Deploy Checklist

## Overview

The watchlist feature adds org/repo monitoring with new-contributor detection, grade-change notifications, a dashboard widget, and weekly email digests. Email sending is gated by `DIGEST_DRY_RUN` (default: `true`) so no emails reach real users until explicitly enabled.

---

## Pre-Deploy Steps

### 1. Run migration on staging

Run `006_watchlist.sql` against a staging database and verify:

- `devtrace_watchlist` and `devtrace_notification_event` tables created
- `issues_opened` and `issues_closed` columns added to `devtrace_contributor_activity`
- Backfill INSERT creates implicit watchlist entries for existing active installations

```sql
SELECT COUNT(*) FROM devtrace_watchlist WHERE source = 'implicit';
```

### 2. Verify CSRF token field name

The settings template uses `{{.CSRFToken}}` in watchlist forms. Confirm the CSRF middleware (`pkg/middleware/csrf.go`) injects a template variable with that exact key. If the key differs, update `settings.html` to match.

### 3. Manual walkthrough

Start the dev server and verify the UI end-to-end:

```sh
make db-up
make seed
make server
```

Then:

- **Settings page** (`/settings`): confirm the Watchlists section renders below Plan
- Add a manual watchlist (enter an org name, click "+ Add Watchlist")
- Verify flash messages appear on redirect (`?msg=...`)
- Toggle email on/off for a watchlist entry
- Delete a manual watchlist (implicit ones should not show a delete button)
- Verify plan limits are enforced (free plan should show "Upgrade" hint, no add form)
- **Dashboard** (`/dashboard`): confirm the "Watchlist Activity" section renders below "Recently Scored"
- If no events exist yet, verify the empty state message appears
- Pagination controls should only appear when `events_total_pages > 1`

### 4. Test email digest (dry run)

Set these env vars and trigger the digest sweep:

```
SEND_API_KEY=<your-resend-key>
BASE_URL=https://devtrace.thingz.io
DIGEST_DRY_RUN=true
DEVTRACE_ADMIN_USERS=<your-github-username>
```

With `DIGEST_DRY_RUN=true` (default), only admin users receive digest emails. Insert a test notification event manually if needed:

```sql
INSERT INTO devtrace_notification_event (watchlist_id, event_type, username, details)
SELECT w.id, 'new_contributor', 'test-user', '{"prs_opened": 3}'::jsonb
FROM devtrace_watchlist w LIMIT 1;
```

Wait for the next ingest cycle to trigger `maybeSendDigests()`, or restart the server. Check logs for `digest sent` or `digest dry run, skipping`.

### 5. Review email content

Verify the received email:

- Subject: "DevTrace Weekly Digest"
- From: `noreply@thingz.io`
- HTML renders correctly (contributor table, badge colors, CTA button)
- Plain text fallback is readable
- "View all activity" links to `/dashboard`
- "Manage watchlists" links to `/settings`
- Contributor names link to `/score/<username>`

### 6. Enable email for all users

Once satisfied with the email content:

```
DIGEST_DRY_RUN=false
```

Deploy with this change. All tenants on Starter/Pro plans with email-enabled watchlists will receive weekly digests.

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `DIGEST_DRY_RUN` | `true` | When `true`, digest emails only go to admin users |
| `SEND_API_KEY` | (none) | Resend API key; digest emails disabled if unset |
| `DEVTRACE_ADMIN_USERS` | (none) | Comma-separated GitHub usernames for admin/dry-run |

---

## Plan Limits

| Capability | Free | Starter | Pro |
|---|---|---|---|
| Implicit watchlist (auto on install) | 1 | 1 | 1 |
| Extra manual watchlists | 0 | 1 | 3 |
| Dashboard widget | yes | yes | yes |
| Weekly email digest | no | yes | yes |
| Event scope: PRs | yes | yes | yes |
| Event scope: PR reviews | no | yes | yes |
| Event scope: Issues + comments | no | no | yes |
