// v0.10.2346 — Messaging (Kafka) grafik panellerinin Metrics açılışları
// panelin ÇİZDİĞİ ifadeyi mi açıyor? Açılış Metrics modeline çözülür,
// Metrics derleyicisiyle PromQL'e çevrilir, panelin ifadesiyle karşılaştırılır.
// Kabul edilen tek fark: partition lag panelinin `topk(10, …)` kırpması
// (Metrics kendi seri sınırını uygular). `or` yedekli istemci düzeyi
// sorgu taşıyan çok-ifadeli panel link ALMAZ.
import { describe, it, expect } from 'vitest';
import { kafkaPanelLink, kafkaPanels, type KafkaScope } from './kafkaMetrics';
import { metricsLinkHref } from './metricsLink';
import { importLegacyMetricsUrl } from './metricsExplorer/legacy';
import { decodeMx, MX_PARAM, type MxModel } from './metricsExplorer/model';
import { compileMxFormula, compileMxQuery } from './metricsExplorer/compile';

function modelOf(href: string): MxModel {
  const sp = new URLSearchParams(href.split('?')[1]);
  const mx = sp.get(MX_PARAM);
  const m = mx ? decodeMx(mx) : importLegacyMetricsUrl(sp)?.model;
  if (!m) throw new Error(`açılamadı: ${href}`);
  return m;
}
const compiled = (m: MxModel) => (m.mode === 'code' ? [m.code] : m.queries.filter(q => !q.hidden).map(q => {
  if (q.kind === 'formula') { const f = compileMxFormula(q.expr, m.queries); return f.ok ? f.expr : `ERR ${q.expr}`; }
  return compileMxQuery(q);
}));

const NO_LINK = ['flow', 'latency', 'errors', 'bytes', 'consumer_churn', 'producer_churn', 'request_rate'];

describe.each<[string, KafkaScope]>([
  ['süzgeçsiz', { service: 'orders' }],
  ['pod + istemci süzgeçli', { service: 'orders', host: 'pod-1', consumer: 'c1', producer: 'p1' }],
])('Kafka panel açılışları (%s)', (_, s) => {
  const charts = kafkaPanels(s).filter(p => p.kind === 'chart');
  const ctx = { service: s.service, range: { preset: '1h' as const }, src: 'vm' as const };

  it('link almayanlar yalnız `or` yedekli çok-ifadeli paneller', () => {
    expect(charts.filter(p => !kafkaPanelLink(p)).map(p => p.id)).toEqual(NO_LINK);
  });

  it('her açılış panelin ifadelerini birebir derler', () => {
    for (const p of charts) {
      const spec = kafkaPanelLink(p);
      if (!spec) continue;
      const got = compiled(modelOf(metricsLinkHref(ctx, spec)!));
      const want = p.queries.map(q => q.expr.replace(/^topk\(10, (.*)\)$/, '$1'));
      if (p.id === 'buffer') {
        expect(got, p.id).toEqual([want[0].replace(/(sum by \(client_id\) \([^)]*\))/g, '($1)')]);
        continue;
      }
      expect(got, p.id).toEqual(want);
    }
  });
});

it('süzgeçsiz lag paneli Overview biçiminde açılır', () => {
  const lag = kafkaPanels({ service: 'orders' }).find(p => p.id === 'lag')!;
  expect(metricsLinkHref({ service: 'orders', range: { preset: '1h' }, rangeParam: '3h' }, kafkaPanelLink(lag)!))
    .toBe('/metrics?metric=kafka_consumer_records_lag_max&service=orders&agg=max&by=client_id&range=3h');
});
