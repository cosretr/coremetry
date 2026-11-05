// filterBarSearch.pin — v0.10.1332 (operatör onaylı mockup "Filtre barı ·
// Search son filtrenin arkasında").
//
// Ne çiviliyor (kaynak): üç liste sayfasının filtre barında
//   · Search SON filtrenin hemen arkasında, md boy (size="sm" yok — üç barda
//     tek Search boyu) ve yanında "Enter ↵" ipucu (`EnterHint`);
//   · Services: Search Δ prior'dan SONRA, Reset'ten ÖNCE; Reset sağda
//     (`pc-push`); Search sarmalı `data-pc-pin` (telefonda panele inmez —
//     mekanizma pageControlsCollapse.test'te mount ile çivili);
//   · Traces: trace-id kutusunda "Go" düğmesi YOK (Enter + ✕ kaldı); filtre
//     barında Search Root'tan sonra, boşluk + görünüm anahtarından önce;
//   · Logs: v0.10.1498'de Discover taslağıyla değişti (aşağıdaki blok).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (rel: string) => readFileSync(resolve(__dirname, '..', rel), 'utf8');
const stripComments = (s: string) =>
  s.replace(/\{\/\*[\s\S]*?\*\/\}/g, '').replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');

// İlk `<PageControls` açılışından kapanışına kadar.
function bar(src: string): string {
  const a = src.indexOf('<PageControls');
  expect(a, 'PageControls bulunamadı').toBeGreaterThanOrEqual(0);
  const b = src.indexOf('</PageControls>', a);
  expect(b).toBeGreaterThan(a);
  return src.slice(a, b);
}

// `<Button …>Search</Button>` açılış etiketi (tek primary).
function searchTag(seg: string): string {
  const m = /<Button\b(?:=>|[^>])*?variant="primary"(?:=>|[^>])*?>Search<\/Button>/.exec(seg);
  expect(m, 'Search primary bulunamadı').not.toBeNull();
  return m![0];
}

function at(seg: string, needle: string): number {
  const i = seg.indexOf(needle);
  expect(i, `"${needle}" bulunamadı`).toBeGreaterThanOrEqual(0);
  return i;
}

describe('v0.10.1332 — Services filtre barı', () => {
  const seg = bar(stripComments(read('pages/Services.tsx')));

  it('sıra: picker → Min spans → Min P99 → selectler → Errors only → Δ prior → Search → Reset', () => {
    const order = [
      'placeholder="Filter services…"', 'placeholder="Min spans"', 'placeholder="Min P99 (ms)"',
      'All owner teams', 'All SRE teams', 'All clusters', 'All namespaces',
      'Errors only', 'Δ prior', '>Search</Button>', '>Reset</Button>',
    ].map(n => at(seg, n));
    expect(order).toEqual([...order].sort((x, y) => x - y));
  });

  it('Search md (size="sm" yok), Enter ipucu yanında, sarmal data-pc-pin', () => {
    expect(searchTag(seg)).not.toMatch(/size="sm"/);
    expect(seg).toMatch(/<span className="search-go" data-pc-pin>\s*<Button variant="primary"(?:=>|[^>])*?>Search<\/Button>\s*<EnterHint /);
  });

  it('Reset sağda (pc-push), sarmalın DIŞINDA', () => {
    expect(seg).toMatch(/<Button variant="secondary" className="pc-push" onClick=\{reset\}>Reset<\/Button>/);
    expect(seg.indexOf('>Reset</Button>')).toBeGreaterThan(seg.indexOf('</span>', at(seg, 'search-go')));
  });

  it('Enter iki sayı kutusunda da apply — ipucu dürüst', () => {
    expect((seg.match(/onKeyDown=\{e => \{ if \(e\.key === 'Enter'\) apply\(\); \}\}/g) ?? []).length).toBe(2);
    expect(seg).toMatch(/onEnter=\{apply\}/);
  });
});

describe('v0.10.1332 — Traces', () => {
  const src = stripComments(read('pages/Traces.tsx'));

  // v0.10.1423 — trace-lookup kutusu kalktı (operatör: "Search'ten zaten
  // aranabiliyor"); v0.10.1332'nin Go/✕/Enter pinleri onunla gitti.
  it('trace-lookup yok; operasyon kutusu Enter ile apply', () => {
    expect(src).not.toMatch(/trace-lookup/);
    expect(src).not.toMatch(/aria-label="Trace ID'yi temizle"/);
    expect(src).toMatch(/<OperationPicker[\s\S]{0,400}?onEnter=\{\(\) => apply\(\)\}/);
  });

  it('filtre barı: Search md + ipucu, Root\'tan SONRA, boşluk + görünüm anahtarından ÖNCE', () => {
    const seg = bar(src);
    expect(searchTag(seg)).not.toMatch(/size="sm"/);
    expect(seg).toMatch(/<span className="search-go">\s*<Button variant="primary"(?:=>|[^>])*?>Search<\/Button>\s*<EnterHint /);
    const iRoot = at(seg, '>Root</span>');
    const iSearch = at(seg, '>Search</Button>');
    const iSpacer = at(seg, "<span style={{ marginLeft: 'auto' }} />");
    const iSeg = at(seg, 'aria-label="Trace görünümü"');
    expect(iRoot).toBeLessThan(iSearch);
    expect(iSearch).toBeLessThan(iSpacer);
    expect(iSpacer).toBeLessThan(iSeg);
  });

  // v0.10.1524 (operatör: "'Saved: (none yet…) + Save current view' satırı
  // kalksın") — ayrı şerit (variant 'bar') yok; tek compact "Saved ▾" Reset /
  // CSV ile pageActions kümesinde, o küme grafik başlığının sağında (Volume
  // headerRight + Latency sağ kümesi) ve grafiksiz görünümlerde şeridin sonunda.
  it('kayıtlı görünümler: tek compact mount, Reset/CSV ile aynı küme, ayrı satır yok', () => {
    const mounts = src.match(/<SavedViewsBar\b[^>]*\/>/g) ?? [];
    expect(mounts).toEqual(['<SavedViewsBar page="traces" variant="compact" size="sm" />']);
    expect(src).not.toMatch(/<SavedViewsBar[^>]*\bright=/);
    const acts = src.slice(at(src, 'const pageActions = ('), at(src, '⬇ CSV'));
    const order = ['<SavedViewsBar', '>Reset</Button>', '/api/traces/export.csv?'].map(n => at(acts, n));
    expect(order).toEqual([...order].sort((x, y) => x - y));
    expect((src.match(/\{pageActions\}/g) ?? []).length).toBe(3); // volume, latency, iskelet
    expect(src).toMatch(/options=\{STRIP_STATS\.map\(st => \(\{ value: st, label: st \}\)\)\} \/>\s*\{pageActions\}/);
    expect(bar(src)).toContain("{view !== 'list' && pageActions}");
  });
});

// v0.10.1498 — /logs barı operatör onaylı Kibana-Discover taslağıyla
// (2026-10-24, "Taslağı onaylıyorum") v0.10.1332 mockup'ının YERİNE geçti
// (docs/DECISIONS.md). Çubukta yalnız sorgu: Service · Cluster · KQL ·
// Search/↻ (tek birincil, md, Enter ipucu, telefonda pinli); sağ küme
// PageControls `actions` yuvasında: + Filter ▾ · Live tail · menüler.
// v0.10.1821 (Log Search Faz 1 dilim 1.5) — aynı sıra ve sözleşme, shadcn
// dilimi: çubuk coremetry/FilterBar (PageControls bileşimi), sorgu kutusu
// coremetry/logs/LogQueryInput, birincil = shadcn Button varsayılan varyantı
// (md), Enter ipucu `.kbd`.
describe('v0.10.1498 / v0.10.1821 — Logs sorgu çubuğu (Discover taslağı, shadcn)', () => {
  const src = stripComments(read('pages/logs/LogsQueryBar.tsx'));
  const a0 = src.indexOf('<FilterBar');
  const seg = src.slice(a0, src.indexOf('</FilterBar>', a0));
  const kids = seg.slice(at(seg, '<ServicePicker'));
  const actions = seg.slice(0, at(seg, '<ServicePicker'));

  it('çubuk sırası: Service → Cluster → KQL → Search/↻; başka filtre yok', () => {
    expect(a0, 'FilterBar bulunamadı').toBeGreaterThanOrEqual(0);
    const order = ['<ServicePicker', 'All clusters', '<LogQueryInput', '<span className="search-go" data-pc-pin>']
      .map(n => at(kids, n));
    expect(order).toEqual([...order].sort((x, y) => x - y));
    for (const n of ['Filter logs by trace ID', 'With trace', '>Reset<', '<ShareButton', 'Live tail', 'SavedViewsBar']) {
      expect(kids, n).not.toContain(n);
    }
  });

  it('Search md + ipucu; ↻ aynı yuvada, ikisi de tek birincil (varyant/boy yazılmaz)', () => {
    expect(kids).toMatch(/<span className="search-go" data-pc-pin>\s*\{refreshMode \? \(\s*<Button aria-label="Yenile"/);
    expect(kids).toMatch(/: \(\s*<Button onClick=\{p\.onApply\}>Search<\/Button>\s*\)\}\s*<kbd className="kbd"/);
    expect((kids.match(/<Button\b/g) ?? []).length).toBe(2);
    expect(kids).not.toMatch(/<Button[^>]*\b(variant|size)=/);
  });

  it('sağ küme actions yuvasında: + Filter → Live tail → menüler', () => {
    const order = ['{p.addFilter}', "'▶ Live tail'", '{p.menus}'].map(n => at(actions, n));
    expect(order).toEqual([...order].sort((x, y) => x - y));
    const page = stripComments(read('pages/Logs.tsx'));
    const menus = page.slice(at(page, 'menus={('));
    const m = ['<SavedViewsBar page="logs" variant="compact" />', '<LogsShareMenu', '<LogsMoreMenu']
      .map(n => at(menus, n));
    expect(m).toEqual([...m].sort((x, y) => x - y));
    expect(page).toContain('addFilter={(\n            <LogsAddFilterMenu');
  });

  it('sağa itme tek yerde: barda satır-içi marginLeft: auto yok', () => {
    expect(seg).not.toMatch(/marginLeft:\s*'auto'/);
  });
});

describe('v0.10.1332 — CSS', () => {
  const css = read('styles/globals.css');
  const block = css.slice(css.indexOf('v0.10.1332 BEGIN'), css.indexOf('v0.10.1332 END'));

  it('işaretli tek blok, .kbd token\'larla (10px mono, 1px kenar, 2px alt kenar)', () => {
    expect(block.length).toBeGreaterThan(0);
    const kbd = /\.kbd \{([^}]*)\}/.exec(block)?.[1] ?? '';
    expect(kbd).toMatch(/font-family: var\(--font-mono\)/);
    expect(kbd).toMatch(/font-size: var\(--fs-2xs\)/);
    expect(kbd).toMatch(/border: 1px solid var\(--border-strong\)/);
    expect(kbd).toMatch(/border-bottom-width: 2px/);
    expect(kbd).not.toMatch(/#[0-9a-f]{3,8}\b/i);
  });

  it('.pc-right sağa itiyor; telefonda panelde sıfırlanıyor', () => {
    expect(block).toMatch(/\.pc-right \{[^}]*margin-left: auto/);
    expect(block).toMatch(/\.pc-push \{ margin-left: auto; \}/);
    expect(block).toMatch(/@media \(max-width: 640px\) \{[\s\S]*\.pc-panel \.pc-right,\s*\.pc-panel \.pc-push \{ margin-left: 0; \}/);
  });
});
