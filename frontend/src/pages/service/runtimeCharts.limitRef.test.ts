import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// S0 karakterizasyon (docs/frontend/service-tabs-plan.md §5, K11–K13):
// JVM bellek kartlarında limit serisi referans (ref) ve KESİK çizgi; pod
// başına çizgi tavanı 40 (v0.9.95 operatör: "sadece 8 pod gösteriyor"),
// tavan yalnız ÇİZİMDE uygulanır (fetch tüm serileri getirir); pod çizgi
// etiketi podLineLabel sözleşmesinden. S9 heap kartını zarfa çevirirken
// "Pod başına çizgiler" kipi bu sözleşmeyi korur.
const src = readFileSync(resolve(__dirname, 'RuntimeCharts.tsx'), 'utf8');

describe('RuntimeCharts limit / tavan / etiket (S0)', () => {
  it('heap ve non-heap limit serisi ref:true', () => {
    const refs = src.match(/metric: 'jvm\.memory\.limit'[^}]*ref: true/g) ?? [];
    expect(refs.length).toBe(2);
  });
  it('referans çizgi kesik [5, 4]', () => {
    expect(src).toContain('dash: line.ref ? [5, 4] : undefined');
  });
  it('pod tavanı 40, kesim çizimde, not "N / M pod"', () => {
    expect(src).toContain('const FANOUT_MAX = 40;');
    expect(src).toContain('data.slice(0, FANOUT_MAX)');
    expect(src).toContain('`${FANOUT_MAX} / ${podTotal} pod gösteriliyor`');
  });
  it('pod çizgileri podLineLabel ile etiketlenir', () => {
    expect((src.match(/labelOf: podLineLabel/g) ?? []).length).toBeGreaterThanOrEqual(5);
  });
});

// v0.10.2148 (S9; plan K12 — operatör kararı 2026-11-14, seçenek B): heap
// kartının varsayılanı özet görünüm; yukarıdaki sözleşme (40 tavan, kesik
// limit, podLineLabel) `?rtview=lines` kipinde aynen yaşar ve özet de aynı
// etiketi kullanır.
describe('RuntimeCharts heap görünümü (S9)', () => {
  it('görünüm URL\'den (?rtview), yalnız JVM heap kartı özetlenir', () => {
    expect(src).toContain("const rtRaw = params.get('rtview');");
    expect(src).toContain("family === 'jvm' && card.key === 'heap' && !view.lines;");
  });
  it('özet TÜM pod\'ları alır (kesim yok), etiket aynı sözleşmeden, sınır yumuşatılmaz', () => {
    expect(src).toMatch(/if \(summaryMode\) \{[\s\S]*?for \(const s of data\) \{[\s\S]*?line\.labelOf \? line\.labelOf\(s\.groupKey, line\.label\)/);
    expect(src).toContain('withClusterPrefix(base, podClusterOf(s.groupKey), multi)');
    expect(src).toContain('summary = pct.length > 0 ? heapSummary(pct, true, smoothPoints) : heapSummary(absPods, false, smoothPoints);');
    // Yüzde okumaları pod başına TOPLAM (havuzların toplamı / toplamı); ortalama değil.
    expect(src.match(/agg: 'sum', role: 'pct(Used|Limit)'/g)?.length).toBe(2);
  });
  it('geçiş gerçek bağlantı, replace; ayar / localStorage yok', () => {
    expect(src).toContain('<Link replace to={{ search: rtViewSearch(params, { ...view, lines: true }) }}>Pod başına çizgiler</Link>');
    expect(src).toContain('<Link replace to={{ search: rtViewSearch(params, { ...view, lines: false }) }}>Özet görünüm</Link>');
    expect(src).not.toMatch(/localStorage|STORAGE_KEYS/);
  });
});

// v0.10.2149 (S10) — non-heap düzse cümle; yalnız JVM non-heap kartı aday,
// düzlük ham + kesilmemiş kümeden ve okumalar oturunca söylenir.
describe('RuntimeCharts non-heap düzlük (S10)', () => {
  it('aday yalnız JVM non-heap; okumalar oturmadan "düz" denmez', () => {
    expect(src).toContain("const isFlatCandidate = (family: string | null, card: CardSpec) => family === 'jvm' && card.key === 'nonheap';");
    expect(src).toContain("isFlatCandidate(family, card) && status === 'ready' ? flatSummary(flatInput) : null;");
  });
  it('cümle yalnız varsayılan görünümde; nonheap / lines grafiği açar', () => {
    expect(src).toContain('flat && !view.lines && !view.nonheap ? (');
  });
});

