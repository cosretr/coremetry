import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.454 (operatör 2026-09-06) — /endpoints listesi VARSAYILAN SPAN;
// ?src=metric zorlar; kendiliğinden span'a düşme (v0.10.361) kalktı.
// v0.10.788 (ekip isteği, operatör onayı 2026-09-19) — DETAY varsayılanı
// METRİK (VM); ?src=span zorlar; entry=rpc'de kaynak span. Liste değişmedi.
describe('endpoints source defaults (v0.10.454 + v0.10.788)', () => {
  const list = readFileSync(resolve(__dirname, 'Endpoints.tsx'), 'utf8');
  const detail = readFileSync(resolve(__dirname, 'EndpointDetail.tsx'), 'utf8');
  it('list page defaults to span and only explicit ?src=metric forces metric', () => {
    expect(list).toContain("const src: 'span' | 'metric' = entry === 'rpc' ? 'span'\n    : explicitSrc === 'metric' ? 'metric'\n    : 'span';");
    expect(list).not.toContain('autoSpan');
    // v0.10.1359 — metrik kipi /api/endpoints/metric okumaz (sunucu notu yok):
    // satırlar sekmenin PromQL'inden, servis zorunlu; useEndpoints yalnız span'de.
    expect(list).not.toContain('rowsQ.data?.note');
    expect(list).toContain("const metricMode = src === 'metric';");
    expect(list).toContain('}, !metricMode);');
    // v0.10.1377 — env/cluster seçiliyse ortam eşleyicisi çözülene dek bekler.
    // v0.10.1429 — aile (HTTP / gRPC) çözülene dek de bekler; gRPC satırları ayrı okuma (toplanmaz).
    expect(list).toContain('const metricOn = metricMode && !!service && fam.ready && httpEnvScopeReady(envScope);');
    expect(list).toContain('const metricRes = useHttpPromql(metricItems, { from, to }, metricOn);');
    expect(list).toContain('const grpcRes = useHttpPromql(grpcItems, { from, to }, metricOn);');
  });
  it('detail page defaults to metric, ?src=span forces span, rpc entries stay span', () => {
    expect(detail).toContain("params.get('src') === 'span' || entry ? 'span' : 'metric'");
    // v0.10.1352 — metrik kipinde satır (/api/endpoints/metric) okunmaz;
    // şerit + grafikler sekmenin PromQL'i (useHttpPromql, yalnız metrikte).
    expect(detail).toContain("useEndpoints(refObj && detailSrc === 'span' ? {");
    expect(detail).toContain("}, detailSrc === 'span');");
    expect(detail).toContain("useHttpPromql(httpItems, { from, to }, detailSrc === 'metric' && !!refObj && fam.ready && httpEnvScopeReady(envScope))"); // v0.10.1429 aile
    expect(detail).toContain('<option value="metric">Kaynak: metrik</option>');
    // v0.10.1350 → v0.10.1352 (operatör bildirimi) — metrik kipinde TÜM RED
    // karoları ve grafikler VM'den (sekmenin sorguları); alt bölümler span.
    expect(detail).toContain('Tüm RED karoları ve grafikler: VictoriaMetrics (HTTP/RPC sekmesiyle aynı sorgular');
    expect(detail).toContain('histogram, durum kırılımı, çağıranlar ve trace bölümleri span türevli');
    expect(detail).not.toContain('RED şeridi ve Calls/Errors/latency grafikleri: metrik');
    // tdigest iddiası yalnız span kipinde.
    expect(detail).toContain("' P50/P95/P99 are true window quantiles (tdigest).'}");
    expect(detail).not.toContain('grafikler ve alt bölümler span türevli');
    expect(detail).toContain("next.set('src', 'span'); else next.delete('src');");
  });
});
