-- Sorgu A. Heuristik SINIF, zemin gerçeği DEĞİL. Sabitler: 30 gün, bigBreach 2.0 (problem_priority
-- varsayılanı), flap 15 dk, kopya penceresi ±30 dk (incident.go:391 ile aynı). İlk eşleşen sınıf kazanır.
WITH
base AS (
  SELECT id, rule_id, rule_name, severity, service, metric, value, threshold, comparator, status,
         description, kind, started_at, resolved_at,
    multiIf(
      startsWith(rule_id,'anomaly-auto:'),                                   'anomaly-auto',
      startsWith(rule_id,'anomaly-cluster:ext:') OR startsWith(rule_id,'anomaly:ext') OR kind='external', 'external',
      startsWith(rule_id,'anomaly-cluster:'),                                'anomaly-cluster',
      startsWith(rule_id,'anomaly:') AND endsWith(rule_id,':service_silent'),'service-silent',
      startsWith(rule_id,'anomaly:'),                                        'anomaly',
      startsWith(rule_id,'builtin-'),                                        'builtin',
      startsWith(rule_id,'slo:'),                                            'slo',
      startsWith(rule_id,'db-health:'),                                      'db-health',
      startsWith(rule_id,'db-slow-stmt'),                                    'db-slow-stmt',
      startsWith(rule_id,'db-capacity:'),                                    'db-capacity',
      startsWith(rule_id,'svc-slowdown:'),                                   'svc-slowdown',
      startsWith(rule_id,'runtime:'),                                        'runtime',
      startsWith(rule_id,'exception:shared-dependency'),                     'exception-shared',
      startsWith(rule_id,'exception:fatal-infrastructure'),                  'exception-fatal',
      rule_id = 'exception-storm',                                           'exception-storm',
      startsWith(rule_id,'self-'),                                           'self',
      startsWith(rule_id,'monitor:'),                                        'monitor',
      'rule') AS family,
    -- computePriority ikizi (problem.go:499-527): '<' kuralında 1/oran
    if(threshold > 0,
       if(comparator IN ('<','<='), if(value > 0, threshold / value, 0), value / threshold), 0) AS ratio,
    dateDiff('minute', started_at, coalesce(resolved_at, now64(9))) AS open_min,
    multiIf(
      position(description,'source silent')>0 OR position(description,'signal loss')>0,         'silent',
      position(description,'merged into')>0,                                                   'merged',
      position(description,'rule disabled')>0 OR position(description,'disabled by default')>0
        OR position(description,'problems disabled')>0,                                        'disabled',
      position(description,'anomaly cleared')>0,                                               'cleared',
      position(description,'anomaly muted')>0,                                                 'muted',
      position(description,'batch servis')>0,                                                  'batch',
      position(description,'system excluded')>0,                                               'excluded',
      position(description,'problem mode off')>0,                                              'mode_off',
      position(description,'watch source broken')>0,                                           'watch_broken',
      status = 'resolved',                                                                     'recovered_or_unknown',
      'open') AS resolution,
    position(description,'kaynak: VictoriaMetrics')>0 AS src_vm,
    position(description,'kaynak: span')>0            AS src_span,
    if(startsWith(rule_id,'anomaly-auto:'), substring(rule_id, 14, 16), '') AS event_fp
  FROM problems FINAL
  WHERE started_at >= now64(9) - INTERVAL 30 DAY AND started_at < now64(9)
  ORDER BY started_at
  LIMIT 200000
),
ev AS (
  SELECT id AS fp, kind AS ev_kind, pattern AS ev_pattern, peak_ratio, current_count, episode_count
  FROM anomaly_events FINAL
  WHERE last_seen >= now64(9) - INTERVAL 31 DAY
  LIMIT 100000
),
w AS (
  SELECT b.*, ev.ev_kind, ev.ev_pattern, ev.peak_ratio, ev.current_count, ev.episode_count,
    lagInFrame(resolved_at, 1) OVER (PARTITION BY rule_id, service ORDER BY started_at
      ROWS BETWEEN 1 PRECEDING AND 1 PRECEDING) AS prev_resolved,
    count() OVER (PARTITION BY service ORDER BY toUnixTimestamp(started_at)
      RANGE BETWEEN 1800 PRECEDING AND 1800 FOLLOWING) AS dup_n,
    uniqExact(family) OVER (PARTITION BY service ORDER BY toUnixTimestamp(started_at)
      RANGE BETWEEN 1800 PRECEDING AND 1800 FOLLOWING) AS dup_families,
    (toDayOfWeek(started_at, 0, 'Europe/Istanbul') >= 6 OR toHour(started_at, 'Europe/Istanbul') < 6) AS off_hours
  FROM base b LEFT JOIN ev ON ev.fp = b.event_fp
),
classified AS (
  SELECT *,
    multiIf(
      family IN ('builtin','slo') OR resolution = 'disabled',                              'b_static_disabled',
      family = 'external',                                                                   'f_external',
      family = 'service-silent',                                                             'a_low_traffic',
      resolution = 'silent',                                                                 'a_low_traffic_silent',
      family = 'anomaly' AND metric = 'error_rate' AND threshold > 0 AND threshold < 0.5,   'a_low_traffic',
      family = 'anomaly' AND ratio >= 20,                                                    'a_low_traffic_hugeratio',
      family = 'anomaly' AND metric = 'request_rate',                                        'e_direction',
      family = 'anomaly-auto' AND ev_kind = 'trace_op' AND ev_pattern LIKE '%error_spike%',  'e_direction_count',
      family = 'anomaly-auto' AND ev_kind IN ('trace_op','log_pattern') AND current_count < 50, 'b_count_as_ratio',
      family = 'anomaly-auto' AND ev_kind = 'behavior_change',                                'd_behaviour_onset',
      prev_resolved IS NOT NULL AND dateDiff('minute', prev_resolved, started_at) BETWEEN 0 AND 15, 'd_flapping',
      status = 'resolved' AND open_min <= 10,                                                'd_single_window',
      family IN ('exception-fatal','exception-storm','exception-shared','monitor','runtime',
                 'db-slow-stmt','db-capacity','svc-slowdown','self'),                        'b_static_family',
      dup_families >= 3,                                                                     'h_duplicate_wave',
      src_span = 1 AND family IN ('anomaly','anomaly-cluster'),                              'h_sampled_source',
      family = 'anomaly' AND off_hours = 1 AND status = 'resolved' AND open_min <= 60,       'c_seasonality_suspect',
      family IN ('db-health','rule','anomaly-cluster','anomaly','anomaly-auto'),             'g_or_true_unclassified',
      'unclassified') AS cls
  FROM w
)
SELECT family, cls, count() AS n,
       round(100 * n / sum(n) OVER (), 1)                   AS pct_all,
       round(100 * n / sum(n) OVER (PARTITION BY family), 1) AS pct_family,
       countIf(severity = 'critical') AS critical_n,
       countIf(ratio >= 2) AS big_breach_n,
       round(avg(open_min), 0) AS avg_open_min
FROM classified
GROUP BY family, cls
ORDER BY family, n DESC
LIMIT 500
SETTINGS max_execution_time = 60;
