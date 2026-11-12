import { describe, it, expect } from 'vitest';
import { applyPanelQuery, mxFromLegacyMetricPanel, panelQueryState, panelQueryText, resolvePanelQuery } from './panelQuery';
import { compileMxQuery } from './compile';
import type { MetricPanelConfig, Panel } from '@/lib/types';

// v0.10.2016 (Dashboards Faz 3 dilim 3.7 kalanı) — eski panel config →
// Metrics Explorer satırı. Kayıtlı panel düzenleyicide AYNI sorguyu açmalı.
const metric = (cfg: MetricPanelConfig): Panel => ({ id: 'p', type: 'metric', title: 't', width: 2, config: cfg });

describe('mxFromLegacyMetricPanel — tablo', () => {
  const cases: { name: string; cfg: MetricPanelConfig; type?: string; want: string }[] = [
    { name: 'gösterge avg',
      cfg: { metricName: 'jvm_memory_used_bytes', agg: 'avg' },
      want: 'avg(jvm_memory_used_bytes)' },
    { name: 'histogram avg = mean (v0.10.1887): taban ad VM\'de seri değil',
      cfg: { metricName: 'http_server_request_duration_seconds', agg: 'avg', groupBy: 'http.route' }, type: 'histogram',
      want: 'sum by (http_route) (rate(http_server_request_duration_seconds_sum[$__rate_interval])) / sum by (http_route) (rate(http_server_request_duration_seconds_count[$__rate_interval]))' },
    { name: 'histogram _bucket adıyla kaydedilmiş avg da tabana iner',
      cfg: { metricName: 'http_server_request_duration_seconds_bucket', agg: 'avg' },
      want: 'sum (rate(http_server_request_duration_seconds_sum[$__rate_interval])) / sum (rate(http_server_request_duration_seconds_count[$__rate_interval]))' },
    { name: 'tür bilinmiyor + taban ad: son ekten gösterge sayılır (düzenleyici türü ad zarfından okur)',
      cfg: { metricName: 'http_server_request_duration_seconds', agg: 'avg' },
      want: 'avg(http_server_request_duration_seconds)' },
    { name: 'p95 → histogram_quantile(_bucket)',
      cfg: { metricName: 'http_server_request_duration_seconds', agg: 'p95', service: 'api' },
      want: 'histogram_quantile(0.95, sum by (le, vmrange) (rate(http_server_request_duration_seconds_bucket{service_name="api"}[$__rate_interval])))' },
    { name: 'sayaç rate + noktalı süzgeç anahtarı',
      cfg: { metricName: 'kafka_consumer_records_consumed_total', agg: 'rate', filters: JSON.stringify([{ k: 'resource.k8s.namespace.name', op: '=', v: ['prod'] }]) },
      want: 'sum(rate(kafka_consumer_records_consumed_total{k8s_namespace_name="prod"}[$__rate_interval]))' },
  ];
  for (const c of cases) {
    it(c.name, () => {
      expect(compileMxQuery(mxFromLegacyMetricPanel(c.cfg, [], c.type))).toBe(c.want);
    });
  }
  it('çevrilemeyen süzgeç ve bozuk JSON nota düşer', () => {
    const notes: string[] = [];
    mxFromLegacyMetricPanel({ metricName: 'x', filters: JSON.stringify([{ k: 'a', op: 'IN', v: [] }]) }, notes);
    mxFromLegacyMetricPanel({ metricName: 'x', filters: '{bozuk' }, notes);
    expect(notes.length).toBe(2);
  });
});

describe('panelQueryState', () => {
  it('builder biçimindeki MetricsQL builder satırında açılır; değilse kod kipi', () => {
    const b = panelQueryState({ id: 'q', type: 'promql', title: '', width: 2, config: { query: 'sum by (service_name) (rate(x_total[5m]))' } });
    expect(b.mode).toBe('builder');
    expect(b.query.metric).toBe('x_total');
    const c = panelQueryState({ id: 'q', type: 'promql', title: '', width: 2, config: { query: 'a / b' } });
    expect(c.mode).toBe('code');
    expect(c.code).toBe('a / b');
  });
  it('eski builder metric paneli: derlenmiş metin aynı satırı geri verir', () => {
    const st = panelQueryState(metric({ metricName: 'jvm_memory_used_bytes', agg: 'max', groupBy: 'jvm.memory.pool.name' }));
    expect(st.mode).toBe('builder');
    expect(panelQueryText(st)).toBe('max by (jvm_memory_pool_name) (jvm_memory_used_bytes)');
  });
});

describe('applyPanelQuery', () => {
  it('metric paneli: builder alanları düşer, sorgu promql\'de; birim / viz / eşik / adım kalır', () => {
    const p = metric({ metricName: 'm', agg: 'avg', service: 's', groupBy: 'a', filters: '[]', unit: 'ms', viz: 'area', step: 60, thresholds: [{ value: 1, color: 'red' }] });
    const out = applyPanelQuery(p, 'avg(m)').config as MetricPanelConfig;
    expect(out).toEqual({ metricName: 'm', promql: 'avg(m)', unit: 'ms', viz: 'area', step: 60, thresholds: [{ value: 1, color: 'red' }] });
  });
  it('promql paneli yalnız query değişir', () => {
    const p: Panel = { id: 'q', type: 'promql', title: '', width: 2, config: { query: 'a', unit: 's', source: 'vm' } };
    expect(applyPanelQuery(p, 'b').config).toEqual({ query: 'b', unit: 's', source: 'vm' });
  });
});

describe('resolvePanelQuery', () => {
  it('yer tutucular adımla çözülür; adım yoksa aralık/300; yer tutucusuz metin aynen', () => {
    expect(resolvePanelQuery('rate(x[$__rate_interval])', 60, 3600)).toBe('rate(x[300s])');
    expect(resolvePanelQuery('increase(x[$__interval])', null, 3600 * 24)).toBe('increase(x[288s])');
    expect(resolvePanelQuery('rate(x[5m])', 60, 3600)).toBe('rate(x[5m])');
  });
});
