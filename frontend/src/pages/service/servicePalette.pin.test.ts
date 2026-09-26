import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { HEAP_STATUS } from './HeapBaselineCard';

// servicePalette.pin.test.ts — v0.10.929 (K5 artıkları, operatör kararı
// v0.10.920/922).
//
// KURAL: sağlıklı/normal durum NÖTR; yeşil yalnız GEÇİŞ (resolved /
// recovered …) ve VERİ (grafik serisi). /service altında bunun hâli:
//   • Hata oranı rozetleri: eşikler DEĞİŞMEDİ (Overview/TopEndpoints/Details
//     >5 err, >1 warn; Operations/ClusterBreakdown >5 err, >0 warn) — yalnız
//     sağlıklı dal b-ok → b-gray.
//   • 'N / N running', 'all running', 'bant içinde', INFO log seviyesi,
//     'MV' kaynak rozeti, "Thanos'tan bağımsız" → nötr.
//   • KPI deltası: iyileşme --text2 (CSS, .ov-delta), yalnız kötüleşme kırmızı.
//   • var(--ok) yalnız Overview'daki OK throughput SERİSİ (veri).
// Kaynak-okuma pini: yorumlar soyulur ki gerekçe metnindeki eski sınıf
// adları testi yanıltmasın.
function stripComments(src: string): string {
  // JSX yorumları + satır başı blok/satır yorumları; string içindeki '/*'
  // (ör. glob metinleri) koda dokunmasın diye genel /*…*/ taraması yok.
  return src
    .replace(/\{\/\*[\s\S]*?\*\/\}/g, m => m.replace(/[^\n]/g, ' '))
    .replace(/^\s*\/\*[\s\S]*?\*\//gm, m => m.replace(/[^\n]/g, ' '))
    .replace(/^\s*\/\/.*$/gm, '');
}
const read = (f: string) => stripComments(readFileSync(resolve(__dirname, f), 'utf8'));
const files = readdirSync(__dirname)
  .filter(f => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f));

describe('/service sade palet — K5 artıkları (v0.10.929)', () => {
  it('hiçbir /service dosyası b-ok (sağlıklı = yeşil rozet) basmaz', () => {
    for (const f of files) expect(read(f), f).not.toMatch(/\bb-ok\b/);
  });

  it("var(--ok) yalnız Overview'daki OK throughput serisi (veri)", () => {
    for (const f of files) {
      const hits = read(f).split('\n').filter(l => l.includes('var(--ok)'));
      if (f === 'Overview.tsx') {
        expect(hits.length, f).toBe(1);
        expect(hits[0]).toMatch(/const okLine: ChartLine = .*label: 'OK'/);
      } else {
        expect(hits, f).toEqual([]);
      }
    }
  });

  it('hata oranı yardımcıları: eşikler aynı, sağlıklı dal b-gray', () => {
    for (const f of ['OverviewTables.tsx', 'TopEndpointsCard.tsx', 'DetailsEndpointsSection.tsx']) {
      expect(read(f), f).toContain("return `badge ${rate > 5 ? 'b-err' : rate > 1 ? 'b-warn' : 'b-gray'}`;");
    }
    const ops = read('OperationsTable.tsx');
    expect(ops).toContain("b-${agg.errorRate > 5 ? 'err' : agg.errorRate > 0 ? 'warn' : 'gray'}");
    expect(ops).toContain("const errCls = op.errorRate > 5 ? 'err' : op.errorRate > 0 ? 'warn' : 'gray';");
    expect(read('ServiceClusterBreakdown.tsx'))
      .toContain("const errCls = c.errorRate > 5 ? 'err' : c.errorRate > 0 ? 'warn' : 'gray';");
  });

  it('normal durum rozetleri nötr: pod fazı, heap bandı, INFO seviyesi', () => {
    expect(read('ServicePodsTable.tsx')).toContain("t.phaseKnown && t.failing > 0 ? 'b-err' : 'b-gray'");
    expect(HEAP_STATUS.ok.tone).toBe('b-gray');
    expect(read('ServiceSignalTabs.tsx')).toMatch(/info: 'b-gray'/);
  });

  // v0.10.929 (K5, lider kararı) — kaynak rozeti: 'MV' normal yol nötr;
  // ham spans geri düşüşü gerçek sapma (p50/p95/seri yok) → amber.
  // Overview'ın "kapsam: tüm span'ler" rozetiyle aynı dil.
  it('kaynak rozetleri: normal yol nötr, spans geri düşüşü amber', () => {
    expect(read('ServiceClusterBreakdown.tsx'))
      .toContain("className={`badge ${q.data.source === 'mv' ? 'b-gray' : 'b-warn'}`}");
    expect(read('Overview.tsx'))
      .toContain("className={`badge ${metricMode ? 'b-info' : usingAllSpans ? 'b-warn' : 'b-gray'}`}");
  });

  // v0.10.929 (K5, lider kararı) — Failure rate karosunun --err şeridi SERİ
  // KİMLİĞİ (veri), durum rengi değil: Throughput --accent, Response time
  // --orange ile aynı rol. Bilinçli istisna; başka karo --err şerit almaz.
  it("KPI şerit renkleri seri kimliği: Failure rate --err tek ve açık istisna", () => {
    const accents = [...read('Overview.tsx').matchAll(/<KpiTile lab=\{?"?([^"}\s]+)[\s\S]*?accent="([^"]+)"/g)]
      .map(m => [m[1], m[2]]);
    expect(accents).toEqual([
      ['rtLabel', 'var(--orange)'],
      ['tputLabel', 'var(--accent)'],
      ['Failure', 'var(--err)'],
    ]);
    // Gerekçe yorumu kaynakta durur (yorum soyulmadan okunur).
    expect(readFileSync(resolve(__dirname, 'Overview.tsx'), 'utf8'))
      .toMatch(/v0\.10\.929 \(K5\) — accent="var\(--err\)" BİLİNÇLİ KALIYOR/);
  });

  // KPI deltası (.ov-delta.down / .up.good → --text2) CSS'te; pini
  // styles/k5HealthNeutral.pin.test.ts. Burada yalnız TSX'in rengi inline
  // yeşile çevirmediği çivilenir (yukarıdaki var(--ok) testi).
});
