import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.715 — Details dilim 1 kaynak pini: altı bölüm başlığı SectionHead
// atomuyla (kaynak rozeti), Endpoints bölümü monte, ToC girdisi, kip URL'de.
const svc = readFileSync(resolve(__dirname, '../Service.tsx'), 'utf8');
const ep = readFileSync(resolve(__dirname, 'DetailsEndpointsSection.tsx'), 'utf8');
const metrics = readFileSync(resolve(__dirname, 'DetailsMetricsSection.tsx'), 'utf8');

describe('Details bölüm başlıkları (v0.10.715; v0.10.2132 S1)', () => {
  // v0.10.2132 (S1, servis sekmeleri planı, mockup operatör onayı 2026-11-13):
  // Clusters + Database açık başlık (SectionHead, v0.10.151); diğer beş bölüm
  // CollapsedSection (varsayılan kapalı satır). Endpoints başlığı gövdeden
  // Service.tsx'e taşındı (kip hook'u useEndpointsMode gövdeyle ortak).
  it('Service.tsx: açık bölümler SectionHead, kapananlar CollapsedSection; ham dtl-sech kalmadı', () => {
    for (const id of ['dtl-clusters', 'dtl-db']) expect(svc).toContain(`<SectionHead id="${id}"`);
    for (const id of ['dtl-endpoints', 'dtl-perf', 'dtl-metrics', 'dtl-latency', 'dtl-runtime']) {
      expect(svc).toContain(`<CollapsedSection id="${id}"`);
    }
    expect(svc).not.toContain('className="dtl-sech"');
    expect(metrics).not.toContain('<SectionHead');
    expect(ep).not.toContain('<SectionHead');
  });
  // v0.10.2133 (S2, mockup onaylı 2026-11-13): ToC rayı (DetailsToc) kalktı —
  // ToC girdisi iddiası düştü; kimlikler aşağıdaki S0 pininde yaşar.
  it('Endpoints bölümü monte + kip URL\'de (replace)', () => {
    expect(svc).toContain('<DetailsEndpointsSection service={svc} range={range} rangeNs={rangeNs} env={env} />');
    expect(ep).toContain("next.set('epmode', 'cluster')");
    expect(ep).toContain('{ replace: true }');
    expect(ep).toContain("storageKey: 'svc-dtl-endpoints'");
    // multi-cluster: kapsam Topbar'dan; tek cluster kapsamında kip gizli
    expect(ep).toContain("const scopeCluster = sp.get('cluster') ?? ''");
    expect(ep).toContain("scopeCluster ? 'combined' : parseEndpointsMode(");
  });
  it('kapalı bölümler kalıcı hafızadan (tek anahtar) + çapa açar; Heatmap eski anahtarı yok', () => {
    expect(svc).toContain('parseOpenSections(getRaw(STORAGE_KEYS.svcDetailsOpen))');
    expect(svc).toContain('setRaw(STORAGE_KEYS.svcDetailsOpen, serializeOpenSections(next))');
    expect(svc).toContain("window.addEventListener('hashchange', go)");
    const heat = readFileSync(resolve(__dirname, 'ServiceLatencyHeatmap.tsx'), 'utf8');
    expect(heat).not.toContain('svcHeatmapCollapsed');
  });
});

// S0 karakterizasyon (docs/frontend/service-tabs-plan.md §5): bölüm
// kimlikleri (dtl-*) ve #deploys çapası derin link / CoSRE product_guide
// hedefidir — S1/S2 (kapalı satır, ToC kalkar) sonrasında da yaşar; sıra
// v0.10.151 operatör kararı (Clusters + Database en üstte). Clusters ve
// Database açık kalır (DBQueriesPanel defaultOpen).
describe('Details bölüm kimlikleri ve sırası (S0)', () => {
  const all = svc + ep + metrics;
  it('dtl-* kimlikleri ve #deploys çapası var', () => {
    for (const id of ['dtl-props', 'dtl-clusters', 'dtl-db', 'dtl-endpoints', 'dtl-perf', 'dtl-metrics', 'dtl-latency', 'dtl-runtime', 'deploys']) {
      expect(all).toMatch(new RegExp(`id="${id}"`));
    }
  });
  it('sıra: Props → Clusters → Database → Endpoints → Performance → Latency → Runtime', () => {
    const at = (s: string) => svc.indexOf(s);
    const order = ['id="dtl-props"', 'id="dtl-clusters"', 'id="dtl-db"', '<DetailsEndpointsSection', 'id="dtl-perf"', 'id="dtl-latency"', 'id="dtl-runtime"', 'id="deploys"'];
    for (const s of order) expect(at(s), s).toBeGreaterThan(-1);
    for (let i = 1; i < order.length; i++) expect(at(order[i]), order[i]).toBeGreaterThan(at(order[i - 1]));
  });
  it('Clusters ve Database açık: kapanır satıra alınmaz', () => {
    expect(svc).toContain('<ServiceClusterBreakdown service={svc} range={range} />');
    expect(svc).toMatch(/<DBQueriesPanel[\s\S]*?defaultOpen \/>/);
  });
});

// v0.10.2133 (S2) — ToC kalktı (dosya + CSS + yerleşim sarmalı); Properties
// düz etiket-değer (KeyValue), satır içi stil yok; #deploys bağlantısı korunur.
describe('Details S2: ToC yok, Properties düz', () => {
  const props = readFileSync(resolve(__dirname, 'DetailsPropsStrip.tsx'), 'utf8');
  const css = readFileSync(resolve(__dirname, '../../styles/globals.css'), 'utf8');
  it('ToC yok', () => {
    expect(svc).not.toContain('DetailsToc');
    expect(css).not.toContain('.dtl-toc');
  });
  it('Properties KeyValue, inline style yok, #deploys bağlantısı', () => {
    expect(props).toContain('<KeyValue ');
    expect(props).not.toContain('style=');
    expect(props).toContain('href="#deploys"');
    expect(svc).toContain('<div id="dtl-props"><DetailsPropsStrip service={svc} range={range} /></div>');
  });
});
