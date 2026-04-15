# Repo-Scoped Search Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Let users type `username in org/repo` in the search box to get repo-scoped scores.

**Architecture:** Parse the single search input client-side in JS, split into username + optional repo, build the navigation URL with `?repo=` query param. Update scorecard template to show repo context in the header. No backend changes needed.

**Tech Stack:** Vanilla JS, Go HTML templates

---

### Task 1: Update JS search parser

**Files:**
- Modify: `pkg/server/static/js/app.js:109-126`

**Step 1: Update the initSearch function**

Replace the current `initSearch` function with input parsing logic:

```javascript
function initSearch() {
  var form = document.getElementById('try-search');
  var input = document.getElementById('try-username');
  if (!form || !input) return;

  form.addEventListener('submit', function(e) {
    e.preventDefault();
    var raw = input.value.trim();
    if (!raw) return;

    var btn = form.querySelector('button');
    if (btn) {
      btn.disabled = true;
      btn.textContent = 'Scoring\u2026';
    }
    input.disabled = true;

    // Parse "username in org/repo" format
    var parts = raw.split(/\s+in\s+/);
    var username = parts[0].trim();
    var repo = parts.length > 1 ? parts[1].trim() : '';

    var url = '/score/' + encodeURIComponent(username);
    if (repo) {
      url += '?repo=' + encodeURIComponent(repo);
    }
    window.location.href = url;
  });
}
```

**Step 2: Verify manually**

Open the app, type `octocat in kubernetes/kubernetes`, click Score.
Expected: navigates to `/score/octocat?repo=kubernetes/kubernetes`.

**Step 3: Commit**

```bash
git add pkg/server/static/js/app.js
git commit -S -m "Parse 'username in org/repo' from search input"
```

---

### Task 2: Update placeholder text in all templates

**Files:**
- Modify: `pkg/server/templates/landing.html:8`
- Modify: `pkg/server/templates/home.html:27`
- Modify: `pkg/server/templates/scorecard.html:6`

**Step 1: Update landing.html**

Change placeholder from:
```
GitHub username (e.g., octocat)
```
to:
```
username or username in org/repo
```

**Step 2: Update home.html**

Same placeholder change on line 27.

**Step 3: Update scorecard.html**

Change placeholder from:
```
Score another contributor...
```
to:
```
username or username in org/repo
```

**Step 4: Commit**

```bash
git add pkg/server/templates/landing.html pkg/server/templates/home.html pkg/server/templates/scorecard.html
git commit -S -m "Update search placeholder to show repo filter syntax"
```

---

### Task 3: Show repo context in scorecard header

**Files:**
- Modify: `pkg/server/templates/scorecard.html:18-28`

**Step 1: Add repo subtitle under the username h1**

After line 19 (`<h1 style="margin:0;">{{.Username}}</h1>`), add:

```html
{{if .RepoContext}}
<div style="font-size:0.85rem;color:var(--text-secondary,#8b949e);margin-top:0.25rem;">
  in <a href="https://github.com/{{.RepoContext.Repo}}" target="_blank" rel="noopener" style="color:var(--text-secondary,#8b949e);text-decoration:underline;">{{.RepoContext.Repo}}</a>
</div>
{{end}}
```

**Step 2: Commit**

```bash
git add pkg/server/templates/scorecard.html
git commit -S -m "Show repo context as subtitle in scorecard header"
```

---

### Task 4: Remove ScoringMode from grade display

**Files:**
- Modify: `pkg/server/templates/scorecard.html:34`

**Step 1: Remove ScoringMode from grade label**

Change line 34 from:
```html
<span class="grade-model-label">{{.ModelVersion}} · {{.ScoringMode}}</span>
```
to:
```html
<span class="grade-model-label">{{.ModelVersion}}</span>
```

**Step 2: Verify the grade area renders cleanly**

Open a scorecard page. Grade area should show score + version only, no "global" label.

**Step 3: Commit**

```bash
git add pkg/server/templates/scorecard.html
git commit -S -m "Remove scoring mode label from grade display"
```
