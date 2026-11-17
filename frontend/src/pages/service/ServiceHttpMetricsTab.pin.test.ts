import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1244 — HTTP Metrics sekmesi: Service'te kayıtlı, kaynak VM'e sabit.
const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');

import { t, tf } from '@/lib/i18n';
describe('HTTP Metrics sekmesi', () => {
  it('Service sekme şeridinde ve render ediliyor', () => {
    const svc = read('../Service.tsx');
    expect(svc).toContain("{ key: 'http',       label: 'HTTP/GRPC' }"); // v0.10.1566 — ad HTTP/GRPC, gRPC eki yok
    // v0.10.1377 — env + Topbar cluster kapsamı sekmeye iner (VM ortam süzgeci).
    expect(svc).toContain("{tab === 'http' && <ServiceHttpMetricsTab service={svc} range={range} env={env} cluster={scopeCluster} />}");
  });
  it('sorgular VictoriaMetrics\'e sabit', () => {
    expect(read('../../lib/api.ts')).toContain("withMetricSource(`/api/metrics/promql?${qs(params)}`, 'vm')");
    // v0.10.1352 — okuma paylaşılan kancada (endpoint sayfası da kullanır).
    // v0.10.1377 — ortam çözülene dek sorgu atılmaz (süzgeçsiz an yok).
    expect(read('ServiceHttpMetricsTab.tsx')).toContain('useHttpPromql(all, win, httpEnvScopeReady(envScope))');
    // v0.10.1429 — aile (HTTP / gRPC) + ortam tek istekte; bölüm başına kapsam.
    expect(read('ServiceHttpMetricsTab.tsx')).toContain('useRedFamily(service, env, cluster, win.to)');
    expect(read('ServiceHttpMetricsTab.tsx')).toContain('sectionEnvScope(sec, fam, env, cluster)');
    expect(read('../../lib/queries/httpMetrics.ts')).toContain('api.httpMetricsPromql(');
    expect(read('../../lib/queries/httpMetrics.ts')).toContain('staleTime: 30_000');
  });
});

// v0.10.1247 — Grafana birebir: panel tipleri ve grafik davranışı.
describe('HTTP Metrics Grafana paritesi', () => {
  const tab = read('ServiceHttpMetricsTab.tsx');
  it('stat / gauge / donut / tablo hücreleri; heatmap yok (v0.10.1258)', () => {
    expect(tab).toContain('<HttpStat label="P95 Latency"');
    expect(tab).toContain('<HttpGauge label="SLI: < 500ms"');
    // v0.10.2305 — başlıklar katalogda (TR değerleri aynı).
    expect(tab).toContain("donut(tf('hm.reqDist', lang, { rt: T.RT })"); // v0.10.1429 — RT: Route | Method (gRPC)
    expect(tab).toContain("donut(t('hm.statusDist', lang)");
    expect(tf('hm.reqDist', 'tr', { rt: 'Route' })).toBe('İstek Dağılımı (Route, Top 10)');
    expect(t('hm.statusDist', 'tr')).toBe('Status Code Dağılımı');
    expect(tab).not.toContain('LatencyHeatmap');
    expect(tab).toContain('<GaugeCell');
    expect(tab).toContain('<ToneCell');
  });
  it('grafikler tüm seriler + açık lejant + sabit renkler', () => {
    // v0.10.1352 — panel gövdesi paylaşılan HttpChartPanel'de.
    const viz = read('httpMetricsViz.tsx');
    expect(tab).toContain('<HttpChartPanel ');
    expect(viz).toMatch(/maxSeries=\{60\} legendDefaultOpen/);
    // v0.10.2055 — çağıran renk haritası vermezse (HTTP sekmesi vermez) kod paleti.
    expect(viz).toContain('seriesColors={seriesColors?.size ? seriesColors : httpSeriesColors(');
  });
});

// v0.10.1352 (operatör bildirimi) — route gecikme paneli yüzdeliği açıkça
// söyler; route etiketsiz seri "GET (route yok)" (çıplak "GET" değil).
describe('HTTP Metrics etiketleri (v0.10.1352)', () => {
  const tab = read('ServiceHttpMetricsTab.tsx');
  it('route gecikme başlığı', () => {
    expect(tab).toContain("chart(tf('hm.latBy', lang, { q: 'P95', rt: T.RT.toLowerCase() }), 'p95ByRoute', MR, 's')"); // v0.10.1429 aile; v0.10.2305 katalog
    expect(tf('hm.latBy', 'tr', { q: 'P95', rt: 'route' })).toBe('P95 gecikme (route)');
    expect(tab).not.toContain('P95 Latency (Route bazında)');
  });
  it('route lejantları methodRouteLegend', () => {
    expect(tab).toContain('const MR: LegendTpl = l => methodRouteLegend(l, false, family, lang);'); // v0.10.1429 — gRPC "pkg.Service/Method"; v0.10.2305 lang
    expect(tab).toContain("chart(tf('hm.chartBy', lang, { c: 'Request Rate', rt: T.RT }), 'rateByRoute', MR, 'req/s')");
    expect(tab).toContain("chart(tf('hm.chartBy', lang, { c: T.err5Chart, rt: T.RT }), 'err5ByRoute', SMR, 'req/s')");
    expect(tf('hm.chartBy', 'tr', { c: 'Request Rate', rt: 'Route' })).toBe('Request Rate (Route bazında)');
    expect(tab).not.toContain("'{{http_request_method}} {{http_route}}'");
  });
});
