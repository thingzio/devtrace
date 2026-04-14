(function() {
  'use strict';

  var THEME_KEY = 'devtrace-theme';

  function getTheme() {
    return localStorage.getItem(THEME_KEY) || 'system';
  }

  function setTheme(theme) {
    localStorage.setItem(THEME_KEY, theme);
    applyTheme(theme);
  }

  function applyTheme(theme) {
    var root = document.documentElement;
    if (theme === 'system') {
      root.removeAttribute('data-theme');
    } else {
      root.setAttribute('data-theme', theme);
    }
  }

  applyTheme(getTheme());

  window.DevTrace = {
    setTheme: setTheme,
    getTheme: getTheme,
  };

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
      '<button class="btn btn-sm btn-danger" onclick="confirmRevoke(\'' + id + '\')">Yes</button> ' +
      '<button class="btn btn-sm" onclick="cancelRevoke(\'' + id + '\')">No</button>';
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
      var username = input.value.trim();
      if (!username) return;
      var btn = form.querySelector('button');
      if (btn) {
        btn.disabled = true;
        btn.textContent = 'Scoring\u2026';
      }
      input.disabled = true;
      window.location.href = '/score/' + encodeURIComponent(username);
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
          return; // trend section stays hidden
        }
        var section = document.getElementById('trend-section');
        if (section) section.style.display = 'block';
        new Chart(canvas, {
          type: 'line',
          data: {
            labels: data.map(function(d) { return d.scored_at.substring(0, 10); }),
            datasets: [{
              label: 'Score',
              data: data.map(function(d) { return d.score; }),
              borderColor: '#0366d6',
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

  document.addEventListener('DOMContentLoaded', function() {
    initSearch();
    initTokens();
    initTrendChart();
  });
})();
