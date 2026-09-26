// k5Leftovers.pin.test.ts — v0.10.929 (K5 artıkları, sayfalar N–Z + alt dizinler).
//
// Operatör kararı K5: sağlıklı/normal hâl NÖTR; yeşil yalnız bir GEÇİŞ
// (resolved/completed, kullanıcının az önce yaptığı eylemin geri bildirimi)
// ve VERİ (grafik serisi) için. Renk başka yerde yalnız sapmada (warn/err)
// ve seçimde/vurguda (accent). Bu bir BAĞLANMA pini (kaynak grep'i):
// temizlenen yüzeylere yeşil geri gelmesin, bilerek bırakılan geçiş/veri
// yeşilleri de sessizce kaybolmasın.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');
// Yorumlar eski tonları tarihçe olarak anıyor; pinler yorumsuz kod üzerinde.
const code = (src: string) => src
  .replace(/\/\*[\s\S]*?\*\//g, '')
  .replace(/^\s*\/\/.*$/gm, '');

const GREEN = /\bb-ok\b|var\(--ok\)|tone="success"|'success'/;

describe('v0.10.929 — normal hâl yeşil değil (tamamen temiz dosyalar)', () => {
  const clean = [
    './Service.tsx', './Slos.tsx', './Rollouts.tsx', './Runbooks.tsx', './Users.tsx',
    './TraceKiosk.tsx', './PublicTrace.tsx', './Shift.tsx', './TraceCompare.tsx', './ServiceMap.tsx',
    './pod/PodTracesTable.tsx', './pod/PodIdentityLine.tsx', './pod/PodContextTables.tsx',
    './trace/KioskSpanPanel.tsx', './databases/detailSections.tsx',
    './adminch/traceHealth.ts', './adminch/replicaConsistency.ts',
  ];
  for (const f of clean) {
    it(`${f}: b-ok / var(--ok) / success tonu yok`, () => {
      expect(code(read(f))).not.toMatch(GREEN);
    });
  }
});

describe('v0.10.929 — trace yüzeyleri /trace başlığıyla aynı (sr-only OK)', () => {
  it('TraceKiosk + PublicTrace: ERROR rozeti ya da sr-only OK', () => {
    for (const src of [read('./TraceKiosk.tsx'), read('./PublicTrace.tsx')]) {
      expect(src).toContain('<span className="badge b-err">ERROR</span>');
      expect(src).toContain('<span className="sr-only">OK</span>');
    }
  });
  it('Pod trace tablosu: sağlıklı satır görsel boş, kelime sr-only', () => {
    const src = read('./pod/PodTracesTable.tsx');
    expect(src).toContain('<span className="badge b-err">error</span>');
    expect(src).toContain('<span className="sr-only">ok</span>');
  });
  it('Kiosk span paneli: SpanDetail/TraceKiosk kuralı — ERROR rozeti ya da sr-only durum', () => {
    const src = code(read('./trace/KioskSpanPanel.tsx'));
    expect(src).toContain('<span className="badge b-err">ERROR</span>');
    expect(src).toContain("<span className=\"sr-only\">{span.statusCode || 'unset'}</span>");
    // Sağlıklı span için görünür (gri) rozet kalmadı.
    expect(src).not.toContain("`badge ${err ? 'b-err' : 'b-gray'}`");
  });
});

describe('v0.10.929 — eşikler aynı, yalnız sağlıklı dal nötr', () => {
  it('endpoint "Where the time goes": db kategori rengi seri paletinden, --warn değil', () => {
    const ep = code(read('./endpoints/detailSections.tsx'));
    expect(ep).toContain("import { seriesPalette } from '@/lib/chartFmt';");
    expect(ep).toContain('const dbTone = seriesPalette()[DB_SERIES_SLOT];');
    expect(ep).toContain("e.kind === 'db' ? dbTone : 'var(--accent2)'");
    expect(ep).toContain('<span className="mono" style={{ color: dbTone }}>{b.name}</span>');
    expect(ep).not.toContain("e.kind === 'db' ? 'var(--warn)'");
  });
  it('endpoint + db hata oranı rozetleri', () => {
    const ep = code(read('./endpoints/detailSections.tsx'));
    expect(ep.match(/r\.errorRate >= 5 \? 'b-err' : r\.errorRate >= 1 \? 'b-warn' : 'b-gray'/g)?.length).toBe(2);
    expect(code(read('./databases/detailSections.tsx')))
      .toContain("c.errorRate > 5 ? 'b-err' : c.errorRate > 0 ? 'b-warn' : 'b-gray'");
  });
  it('/service başlık noktası: kırmızı/amber aynı eşikte, sağlıklı nötr halka + metin alternatifi', () => {
    const src = code(read('./Service.tsx'));
    // Eşikler aynı (>5 kritik, >1 uyarı); sınıf ve etiket tek yerden.
    expect(src).toContain("if (errorRate > 5) return { cls: 'red', label: 'critical' };");
    expect(src).toContain("if (errorRate > 1) return { cls: 'amber', label: 'warning' };");
    expect(src).toContain("return { cls: 'green', label: 'healthy' };");
    // Halka globals'taki .ov-dot.green'den (nötr); satır içi kopya yok.
    expect(src).toContain('<span className={`ov-dot ${headDot.cls}`} style={{ width: 12, height: 12 }}');
    expect(src).not.toContain("boxShadow: 'inset 0 0 0 1px var(--border-strong)'");
    // Renk tek taşıyıcı değil: tooltip + ekran okuyucu etiketi.
    expect(src).toContain('role="img" title={headDot.label} aria-label={headDot.label}');
    const css = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8');
    expect(css).toMatch(/\.ov-dot\.green \{ background: transparent; box-shadow: inset 0 0 0 1px var\(--border-strong\); \}/);
  });
  it('Rollouts reconciler son koşu: ok + skipped nötr, partial uyarı, gerisi hata', () => {
    expect(code(read('./Rollouts.tsx'))).toContain(
      "lastRun.status === 'ok' || lastRun.status === 'skipped' ? 'b-gray' : lastRun.status === 'partial' ? 'b-warn' : 'b-err'");
  });
  it('SLO: Healthy / güvenli yanma / normal yanma nötr, ihlal kırmızı', () => {
    const slos = code(read('./Slos.tsx'));
    expect(slos).toContain('<span className="badge b-gray">Healthy</span>');
    expect(slos).toContain("rate > 2 ? 'b-err' : rate > 1 ? 'b-warn' : 'b-gray'");
    expect(slos).toContain("pct > 50 ? 'var(--text3)' : pct > 20 ? 'var(--warn)' : 'var(--err)'");
    const svc = code(read('./Service.tsx'));
    expect(svc).toContain("noData || healthy ? 'b-gray' : 'b-err'");
    expect(svc).toContain("budget > 0.25 ? 'var(--text3)'");
  });
});

describe('v0.10.929 — açık/kapalı, varlık, OPEN: nötr', () => {
  it('Watchers: ON ve OPEN nötr; kaybolan sapma rengi yok', () => {
    const src = code(read('./Watchers.tsx'));
    expect(src).not.toContain('badge b-ok');
    expect(src).not.toMatch(/badge b-err"[^>]*>OPEN/);
    // v0.10.929 (K5) — OPEN tonu tek durum sözlüğünden (features/anomalies/statusTone).
    expect(src.match(/<TriageStatusBadge s="open" label="OPEN"/g)?.length).toBe(2);
    expect(src).not.toMatch(/className="badge b-\w+"[^>]*>OPEN</);
    expect(src).toContain("<span style={{ color: 'var(--text)' }}>● enabled</span>");
    expect(src).toContain("<span style={{ color: 'var(--text3)' }}>✓ sent</span>");
  });
  it('Runbook ENABLED ve adımın agent rozeti nötr', () => {
    expect(code(read('./Runbook.tsx'))).toContain('<span className="badge b-gray">\n            {draft.enabled ?');
    expect(code(read('./RunbookExecution.tsx'))).toContain('<span className="badge b-gray">agent</span>');
  });
  it('adminstats batch gönderim "etkin" nötr; KAPALI kırmızı', () => {
    expect(code(read('./adminstats/panels.tsx'))).toContain("state.batching.effective ? 'b-gray' : 'b-err'");
  });
  it('Watcher içe aktarma önizlemesi: tam destek nötr', () => {
    expect(code(read('./alerts/WatcherImportModal.tsx'))).toContain("supported:   'b-gray',");
  });
});

describe('v0.10.929 — iyileşme deltası ve "yeni" (lead kararları)', () => {
  it('Shift: iyileşme --text2, eşit değer ok işaretsiz', () => {
    const src = code(read('./Shift.tsx'));
    expect(src).toContain("worse ? 'var(--err)' : 'var(--text2)'");
    expect(src).toContain("cur > base ? ' ↑' : cur < base ? ' ↓' : ''");
  });
  it('TraceCompare: daha hızlı B nötr', () => {
    const src = code(read('./TraceCompare.tsx'));
    expect(src).toContain("deltaNs > 0 ? 'var(--err)' : deltaNs < 0 ? 'var(--text2)' : 'var(--text3)'");
  });
  it('ServiceMap Δ şeridi: iki "+N" çipi vurgu (b-info = --accent2 ailesi), satır içi renk yok', () => {
    const src = code(read('./ServiceMap.tsx'));
    expect(src.match(/className="badge b-info" style=\{\{ marginRight: 6 \}\}>\+\{new(Svcs|Deps)\.length\} (svc|edge)</g)?.length).toBe(2);
    expect(src).not.toContain("color: 'var(--accent2)' }}>+");
  });
});

// Bilerek KALAN yeşiller: geçiş / eylem geri bildirimi / veri serisi.
describe('v0.10.929 — geçiş ve veri yeşilleri korunur', () => {
  it('yaşam döngüsü completed (runbook yürütme/adım) yeşil kalır', () => {
    expect(read('./Runbook.tsx')).toContain("completed: 'b-ok'");
    expect(read('./RunbookExecution.tsx').match(/completed: 'b-ok'/g)?.length).toBe(2);
  });
  it('Watchers geçmişi: resolve noktası yeşil (geçiş)', () => {
    expect(read('./Watchers.tsx')).toContain("e.kind === 'resolve' ? 'var(--ok)'");
  });
  it('içe aktarma sonrası kutu, 2xx veri çubuğu, L1 önbellek serisi', () => {
    expect(read('./alerts/WatcherImportModal.tsx')).toContain("border: '1px solid var(--ok)'");
    // 2xx: ROZET nötr (sağlıklı hâl), ÇUBUK yeşil (veri serisi).
    expect(read('./endpoints/detailSections.tsx')).toContain("{ label: '2xx', n: st.http2xx, cls: 'b-gray', color: 'var(--ok)' }");
    expect(read('./adminstats/panels.tsx')).toContain("'HIT-L1':     'var(--ok)'");
  });
});
