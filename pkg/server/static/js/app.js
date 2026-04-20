(function() {
  'use strict';

  var THEME_KEY = 'devtrace-theme';

  function getTheme() {
    return localStorage.getItem(THEME_KEY) || 'dark';
  }

  function setTheme(theme) {
    localStorage.setItem(THEME_KEY, theme);
    applyTheme(theme);
  }

  function applyTheme(theme) {
    var root = document.documentElement;
    if (theme === 'dark') {
      root.removeAttribute('data-theme');
    } else {
      root.setAttribute('data-theme', theme);
    }
    var btns = document.querySelectorAll('.theme-toggle [data-theme]');
    btns.forEach(function(b) {
      b.classList.toggle('active', b.getAttribute('data-theme') === theme);
    });
  }

  applyTheme(getTheme());

  window.DevTrace = {
    setTheme: setTheme,
    getTheme: getTheme,
  };

  // Nav dropdown toggle and outside-click close.
  function initNavDropdown() {
    var btn = document.querySelector('.nav-profile-btn');
    if (btn) {
      btn.addEventListener('click', function() {
        var dd = document.querySelector('.nav-dropdown');
        if (dd) dd.classList.toggle('open');
      });
    }
    document.addEventListener('click', function(e) {
      var d = document.querySelector('.nav-dropdown');
      if (d && !e.target.closest('.nav-profile')) d.classList.remove('open');
    });
  }

  // Token management (settings page).
  window.generateToken = function() {
    var input = document.getElementById('token-name-input');
    var name = input ? input.value.trim() : '';
    if (!name) {
      if (input) input.focus();
      return;
    }
    var modal = document.getElementById('token-modal');
    fetch('/api/v1/token', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: name }),
    })
      .then(function(r) { return r.json(); })
      .then(function(data) {
        if (modal) modal.style.display = 'none';
        if (input) input.value = '';
        if (data.token) {
          var msg = document.getElementById('new-token-msg');
          var val = document.getElementById('new-token-value');
          if (msg && val) {
            val.textContent = data.token;
            msg.style.display = 'block';
          }
          var table = document.getElementById('token-table');
          var noTokens = document.getElementById('no-tokens');
          if (table) {
            table.style.display = '';
            var tbody = table.querySelector('tbody');
            var tr = document.createElement('tr');
            tr.innerHTML = '<td>' + data.name + '</td><td>Never</td><td>just now</td><td></td>';
            tbody.appendChild(tr);
          }
          if (noTokens) noTokens.style.display = 'none';
        } else {
          var errEl = document.getElementById('token-error');
          if (errEl) {
            errEl.textContent = data.error || 'Unknown error';
            errEl.style.display = 'block';
          }
        }
      });
  };

  window.revokeToken = function(id) {
    var row = document.getElementById('token-' + id);
    if (!row) return;
    var cell = row.querySelector('td:last-child');
    var original = cell.innerHTML;
    cell.innerHTML = '<span style="font-size:0.8rem;margin-right:0.5rem;">Revoke?</span>' +
      '<button class="btn btn-sm btn-danger" data-confirm-revoke="' + id + '">Yes</button> ' +
      '<button class="btn btn-sm" data-cancel-revoke="' + id + '">No</button>';
    cell.dataset.original = original;
  };

  window.cancelRevoke = function(id) {
    var row = document.getElementById('token-' + id);
    if (!row) return;
    var cell = row.querySelector('td:last-child');
    cell.innerHTML = cell.dataset.original;
  };

  window.confirmRevoke = function(id) {
    fetch('/api/v1/token/' + id, { method: 'DELETE' })
      .then(function(r) {
        if (r.ok) {
          var row = document.getElementById('token-' + id);
          if (row) row.remove();
          var tbody = document.querySelector('#token-table tbody');
          if (tbody && tbody.children.length === 0) {
            document.getElementById('token-table').style.display = 'none';
            var noTokens = document.getElementById('no-tokens');
            if (noTokens) noTokens.style.display = '';
          }
        }
      });
  };

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

  function initTokens() {
    var createBtn = document.getElementById('create-token-btn');
    if (!createBtn) return;

    createBtn.addEventListener('click', function() {
      var name = prompt('Token name (e.g., my-ci-token):');
      if (!name) return;

      fetch('/api/v1/token', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: name }),
      })
        .then(function(r) { return r.json(); })
        .then(function(data) {
          if (data.token) {
            alert('Token created (copy now, shown once):\n\n' + data.token);
            location.reload();
          } else {
            alert('Error: ' + (data.error || 'unknown'));
          }
        });
    });

    document.querySelectorAll('.revoke-token').forEach(function(btn) {
      btn.addEventListener('click', function() {
        var id = this.dataset.tokenId;
        if (!confirm('Revoke this token?')) return;
        fetch('/api/v1/token/' + id, { method: 'DELETE' })
          .then(function() { location.reload(); });
      });
    });
  }

  function initTrendChart() {
    var canvas = document.getElementById('trend-chart');
    if (!canvas || typeof Chart === 'undefined') return;
    var username = canvas.dataset.username;
    if (!username) return;

    fetch('/api/v1/score/' + encodeURIComponent(username) + '/history')
      .then(function(r) { return r.ok ? r.json() : []; })
      .then(function(data) {
        if (!data || data.length < 2) {
          return;
        }
        var section = document.getElementById('trend-section');
        if (section) section.style.display = 'block';
        new Chart(canvas, {
          type: 'line',
          data: {
            labels: data.map(function(d) {
              var dt = new Date(d.scored_at);
              return (dt.getMonth() + 1) + '/' + dt.getDate();
            }),
            datasets: [{
              label: 'Score',
              data: data.map(function(d) { return d.score; }),
              borderColor: '#4a9eff',
              tension: 0.3,
              fill: false,
            }]
          },
          options: {
            responsive: true,
            maintainAspectRatio: false,
            scales: {
              y: { min: 0, max: 1, ticks: { color: '#8b949e' } },
              x: { ticks: { color: '#8b949e' } }
            },
            plugins: { legend: { display: false } }
          }
        });
      });
  }

  // Format <time datetime="..."> to user-local time.
  function initLocalTime() {
    document.querySelectorAll('time[datetime]').forEach(function(el) {
      var d = new Date(el.getAttribute('datetime'));
      if (!isNaN(d)) {
        el.textContent = d.toLocaleString(undefined, {
          month: 'numeric', day: 'numeric',
          hour: '2-digit', minute: '2-digit',
        });
      }
    });
  }

  // Help page search filter.
  function initHelpSearch() {
    var input = document.getElementById('help-search');
    if (!input) return;
    var sections = document.querySelectorAll('.help-section');
    var noResults = document.getElementById('help-no-results');
    input.addEventListener('input', function() {
      var q = this.value.toLowerCase().trim();
      var visibleSections = 0;
      sections.forEach(function(s) {
        var heading = s.querySelector('h3');
        var headingMatch = !q || (heading && heading.textContent.toLowerCase().indexOf(q) !== -1);
        var items = s.querySelectorAll('li');
        var visibleItems = 0;
        items.forEach(function(li) {
          var match = !q || headingMatch || li.textContent.toLowerCase().indexOf(q) !== -1;
          li.style.display = match ? '' : 'none';
          if (match) visibleItems++;
        });
        var sectionVisible = !q || headingMatch || visibleItems > 0;
        s.style.display = sectionVisible ? '' : 'none';
        if (sectionVisible) visibleSections++;
      });
      noResults.style.display = visibleSections === 0 ? '' : 'none';
    });
  }

  // Settings page: data-action handlers.
  function initSettingsActions() {
    document.addEventListener('click', function(e) {
      var action = e.target.getAttribute('data-action');
      if (action) {
        switch (action) {
          case 'dismiss-token':
            var msg = document.getElementById('new-token-msg');
            if (msg) msg.style.display = 'none';
            break;
          case 'copy-token':
            var val = document.getElementById('new-token-value');
            if (val) navigator.clipboard.writeText(val.textContent);
            break;
          case 'show-token-modal':
            var modal = document.getElementById('token-modal');
            if (modal) modal.style.display = 'flex';
            break;
          case 'hide-token-modal':
            var modal2 = document.getElementById('token-modal');
            if (modal2) modal2.style.display = 'none';
            break;
          case 'generate-token':
            window.generateToken();
            break;
        }
      }

      var revokeId = e.target.getAttribute('data-revoke-token');
      if (revokeId) window.revokeToken(revokeId);

      var confirmId = e.target.getAttribute('data-confirm-revoke');
      if (confirmId) window.confirmRevoke(confirmId);

      var cancelId = e.target.getAttribute('data-cancel-revoke');
      if (cancelId) window.cancelRevoke(cancelId);

      var theme = e.target.getAttribute('data-theme');
      if (theme) DevTrace.setTheme(theme);
    });
  }

  // Reload page on back/forward cache restore.
  window.addEventListener('pageshow', function(e) { if (e.persisted) location.reload(); });

  // Read a cookie value by name.
  function getCookie(name) {
    var match = document.cookie.match(new RegExp('(?:^|; )' + name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '=([^;]*)'));
    return match ? decodeURIComponent(match[1]) : '';
  }

  // Inject hidden csrf_token fields into all POST forms that don't already have one.
  function initCSRF() {
    var cookieName = document.cookie.indexOf('__Host-csrf=') !== -1 ? '__Host-csrf' : 'csrf';
    var token = getCookie(cookieName);
    if (!token) return;
    document.querySelectorAll('form[method="POST"], form[method="post"]').forEach(function(form) {
      if (form.querySelector('input[name="csrf_token"]')) return;
      var input = document.createElement('input');
      input.type = 'hidden';
      input.name = 'csrf_token';
      input.value = token;
      form.appendChild(input);
    });
  }

  // Plans table: feature description tooltips.
  function initFeatureTooltips() {
    var tooltip = document.getElementById('feature-tooltip');
    if (!tooltip) return;
    var textEl = document.getElementById('feature-tooltip-text');
    var closeBtn = tooltip.querySelector('.feature-tooltip-close');

    document.querySelectorAll('.feature-info').forEach(function(link) {
      link.addEventListener('click', function(e) {
        e.preventDefault();
        var desc = this.getAttribute('data-desc');
        if (!desc) return;
        textEl.textContent = desc;
        // Position tooltip after the clicked row's parent table wrapper.
        var wrap = this.closest('.plans-table-wrap');
        if (wrap) wrap.parentNode.insertBefore(tooltip, wrap.nextSibling);
        tooltip.style.display = 'flex';
      });
    });

    if (closeBtn) {
      closeBtn.addEventListener('click', function() {
        tooltip.style.display = 'none';
      });
    }
  }

  document.addEventListener('DOMContentLoaded', function() {
    initCSRF();
    initNavDropdown();
    initSearch();
    initTokens();
    initTrendChart();
    initLocalTime();
    initHelpSearch();
    initFeatureTooltips();
    initSettingsActions();
    applyTheme(getTheme());
  });
})();
