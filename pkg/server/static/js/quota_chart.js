(function() {
  var chart = null;
  var dangerThreshold = 80;

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

        // Aggregate per timestamp: sum used and limit across all installations.
        var byTime = {};
        samples.forEach(function(s) {
          if (!byTime[s.sampled_at]) {
            byTime[s.sampled_at] = { used: 0, limit: 0 };
          }
          byTime[s.sampled_at].used += s.quota_used;
          byTime[s.sampled_at].limit += s.quota_limit;
        });

        var times = Object.keys(byTime).sort();
        var labels = times.map(fmtTime);
        var pctData = times.map(function(t) {
          return byTime[t].limit > 0 ? Math.round(byTime[t].used / byTime[t].limit * 100) : 0;
        });
        var thresholdData = times.map(function() { return dangerThreshold; });

        var ctx = document.getElementById('quotaChart').getContext('2d');
        if (chart) chart.destroy();
        chart = new Chart(ctx, {
          type: 'line',
          data: {
            labels: labels,
            datasets: [
              {
                label: 'Pool Utilization',
                data: pctData,
                borderColor: '#3b82f6',
                backgroundColor: function(context) {
                  var c = context.chart;
                  var area = c.chartArea;
                  if (!area) return '#3b82f622';
                  var grad = c.ctx.createLinearGradient(0, area.bottom, 0, area.top);
                  grad.addColorStop(0, 'rgba(59,130,246,0.02)');
                  grad.addColorStop(0.8, 'rgba(59,130,246,0.15)');
                  grad.addColorStop(1, 'rgba(239,68,68,0.3)');
                  return grad;
                },
                borderWidth: 2,
                pointRadius: 3,
                pointBackgroundColor: function(context) {
                  var v = context.parsed && context.parsed.y;
                  return v >= dangerThreshold ? '#ef4444' : '#3b82f6';
                },
                tension: 0.3,
                fill: true
              },
              {
                label: 'Throttle Risk (' + dangerThreshold + '%)',
                data: thresholdData,
                borderColor: '#ef4444',
                borderDash: [6, 3],
                borderWidth: 1,
                pointRadius: 0,
                fill: false
              }
            ]
          },
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
                    if (tip.datasetIndex !== 0) return '';
                    var t = times[tip.dataIndex];
                    var d = byTime[t];
                    return comma(d.used) + ' / ' + comma(d.limit) + ' tokens';
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
