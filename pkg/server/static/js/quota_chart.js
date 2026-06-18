(function() {
  var chart = null;
  var dangerThreshold = 80;

  var FAMILIES = [
    { key: 'core',    label: 'Core REST',       color: '#3b82f6', usedKey: 'quota_used',    limitKey: 'quota_limit' },
    { key: 'search',  label: 'Search (30/min)', color: '#eab308', usedKey: 'search_used',   limitKey: 'search_limit' },
    { key: 'graphql', label: 'GraphQL',         color: '#22c55e', usedKey: 'graphql_used',  limitKey: 'graphql_limit' }
  ];

  function fmtTime(iso) {
    var d = new Date(iso);
    var mo = d.toLocaleString('en', {month:'short'});
    var day = d.getDate();
    var hh = String(d.getHours()).padStart(2,'0');
    var mm = String(d.getMinutes()).padStart(2,'0');
    return mo + ' ' + day + ' ' + hh + ':' + mm;
  }

  function comma(n) {
    return n.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  }

  // Aggregate per timestamp: sum used + limit across all installations,
  // per family. A zero limit means "no data" for that family at that
  // timestamp (pre-cutover sample); the line stays at 0 rather than
  // jumping to 100%.
  function aggregate(samples) {
    var byTime = {};
    samples.forEach(function(s) {
      if (!byTime[s.sampled_at]) {
        byTime[s.sampled_at] = {};
        FAMILIES.forEach(function(f) { byTime[s.sampled_at][f.key] = { used: 0, limit: 0 }; });
      }
      FAMILIES.forEach(function(f) {
        byTime[s.sampled_at][f.key].used  += s[f.usedKey]  || 0;
        byTime[s.sampled_at][f.key].limit += s[f.limitKey] || 0;
      });
    });
    return byTime;
  }

  function loadQuotaHistory(hours) {
    fetch('/admin/tokens/quota-history?hours=' + hours)
      .then(function(r) { return r.json(); })
      .then(function(samples) {
        if (!samples || samples.length === 0) {
          document.getElementById('quota-empty').style.display = 'block';
          if (chart) { chart.destroy(); chart = null; }
          return;
        }
        document.getElementById('quota-empty').style.display = 'none';

        var byTime = aggregate(samples);
        var times = Object.keys(byTime).sort();
        var labels = times.map(fmtTime);

        var datasets = FAMILIES.map(function(f) {
          var pctData = times.map(function(t) {
            var d = byTime[t][f.key];
            return d.limit > 0 ? Math.round(d.used / d.limit * 100) : 0;
          });
          return {
            label: f.label,
            data: pctData,
            borderColor: f.color,
            backgroundColor: f.color + '22',
            borderWidth: 2,
            pointRadius: 2,
            pointBackgroundColor: function(context) {
              var v = context.parsed && context.parsed.y;
              return v >= dangerThreshold ? '#ef4444' : f.color;
            },
            tension: 0.3,
            fill: false,
            // Hidden by default for graphql so the busy lines (core/search)
            // are easier to read; users can toggle via the legend.
            hidden: f.key === 'graphql'
          };
        });

        datasets.push({
          label: 'Throttle Risk (' + dangerThreshold + '%)',
          data: times.map(function() { return dangerThreshold; }),
          borderColor: '#ef4444',
          borderDash: [6, 3],
          borderWidth: 1,
          pointRadius: 0,
          fill: false
        });

        var ctx = document.getElementById('quotaChart').getContext('2d');
        if (chart) chart.destroy();
        chart = new Chart(ctx, {
          type: 'line',
          data: { labels: labels, datasets: datasets },
          options: {
            responsive: true,
            maintainAspectRatio: false,
            interaction: { mode: 'index', intersect: false },
            scales: {
              x: {
                grid: { color: 'rgba(128,128,128,0.15)' },
                ticks: { maxTicksLimit: 12 }
              },
              y: {
                min: 0,
                max: 100,
                grid: { color: 'rgba(128,128,128,0.15)' },
                ticks: { callback: function(v) { return v + '%'; } }
              }
            },
            plugins: {
              tooltip: {
                callbacks: {
                  afterLabel: function(tip) {
                    var family = FAMILIES[tip.datasetIndex];
                    if (!family) return '';
                    var t = times[tip.dataIndex];
                    var d = byTime[t][family.key];
                    if (d.limit === 0) return 'no data';
                    return comma(d.used) + ' / ' + comma(d.limit) + ' calls';
                  }
                }
              },
              legend: { position: 'top' }
            }
          }
        });
      })
      .catch(function(err) {
        console.error('quota history fetch error:', err);
        document.getElementById('quota-empty').style.display = 'block';
      });
  }

  document.getElementById('quota-hours').addEventListener('change', function() {
    loadQuotaHistory(this.value);
  });

  loadQuotaHistory(24);
})();
