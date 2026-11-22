// v0.10.2346 — Messaging (Kafka) grafik panellerinin Metrics açılışları
// panelin ÇİZDİĞİ ifadeyi mi açıyor? Açılış Metrics modeline çözülür,
// Metrics derleyicisiyle PromQL'e çevrilir, panelin ifadesiyle karşılaştırılır.
// Kabul edilen tek fark: partition lag panelinin `topk(10, …)` kırpması
// (Metrics kendi seri sınırını uygular).
//
// v0.10.2467 (operatör: "bazı Messaging grafikleri Metrics'te açılmıyor") —
// kök neden: builder'ın ifade edemediği paneller (`or` yedekli istemci düzeyi,
// çok-ifadeli hata paneli) hiç link almıyordu → tık ölüydü. Artık HER grafik
// paneli açılır: builder ya da birebir PromQL (kod kipi, çok ifade
// label_replace + or). Topic süzgeci açıkken de.
import { describe, it, expect } from 'vitest';
import { kafkaCodeOf, kafkaPanelLink, kafkaPanels, type KafkaScope } from './kafkaMetrics';
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

const CODE = ['flow', 'latency', 'errors', 'bytes', 'consumer_churn', 'producer_churn', 'fetch_size', 'batch_size', 'request_rate'];

describe.each<[string, KafkaScope]>([
  ['süzgeçsiz', { service: 'orders-api' }],
  ['pod + istemci süzgeçli', { service: 'orders-api', host: 'pod-1', consumer: 'c1', producer: 'p1' }],
  ['topic süzgeçli', { service: 'orders-api', host: 'pod-1', topic: 'payments-topic' }],
])('Kafka panel açılışları (%s)', (_, s) => {
  const charts = kafkaPanels(s).filter(p => p.kind === 'chart');
  const ctx = { service: s.service, range: { preset: '1h' as const }, src: 'vm' as const };

  it('her grafik paneli açılır (ölü tık yok)', () => {
    for (const p of charts) expect(metricsLinkHref(ctx, kafkaPanelLink(p)!), p.id).toBeTruthy();
  });

  it('her açılış panelin ifadelerini birebir derler', () => {
    for (const p of charts) {
      const spec = kafkaPanelLink(p)!;
      const got = compiled(modelOf(metricsLinkHref(ctx, spec)!));
      const want = p.queries.map(q => q.expr.replace(/^topk\(10, (.*)\)$/, '$1'));
      if (p.id === 'buffer' && !s.topic) {
        expect(got, p.id).toEqual([want[0].replace(/(sum by \(client_id\) \([^)]*\))/g, '($1)')]);
        continue;
      }
      if ('code' in spec) {
        expect(got, p.id).toEqual([kafkaCodeOf(p.queries)]);
        continue;
      }
      expect(got, p.id).toEqual(want);
    }
  });

  if (!s.topic) {
    it('kod kipinde açılanlar yalnız builder\'ın ifade edemedikleri', () => {
      expect(charts.filter(p => 'code' in kafkaPanelLink(p)!).map(p => p.id)).toEqual(CODE);
    });
  }
});

it('çok ifadeli panel: her seri ayrı etiketle, hepsi kodda', () => {
  const flow = kafkaPanels({ service: 'orders-api' }).find(p => p.id === 'flow')!;
  const code = kafkaCodeOf(flow.queries);
  expect(code.startsWith('label_replace((sum by (client_id)')).toBe(true);
  expect(code).toContain('"series", "produce", "", "")');
  expect(code).toContain('"series", "consume", "", "")');
  for (const q of flow.queries) expect(code).toContain(q.expr);
});

it('süzgeçsiz lag paneli Overview biçiminde açılır', () => {
  const lag = kafkaPanels({ service: 'orders' }).find(p => p.id === 'lag')!;
  expect(metricsLinkHref({ service: 'orders', range: { preset: '1h' }, rangeParam: '3h' }, kafkaPanelLink(lag)!))
    .toBe('/metrics?metric=kafka_consumer_records_lag_max&service=orders&agg=max&by=client_id&range=3h');
});
