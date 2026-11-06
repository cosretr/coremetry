import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { jsxOpenTags } from '@/styles/jsxTags';

// v0.10.451 (dış skill denetimi D3, kalan siteler) — tıklanabilir <tr>
// yalnız rowActivation ile (klavye eşdeğeri: role=button, tabIndex=0,
// Enter/Space). Kaynak taraması: sayfa/bileşen ağacında `<tr … onClick=`
// KALMAMALI. Tek muafiyet VirtualTable primitifi (onRowClick sözleşmesi;
// Traces gibi tüketiciler kendi rowActivation'ını uygular).
// v0.10.455 (dilim 3): tarama ÇOK SATIRLI açılış etiketlerini de kapsar.
// Olayı okuyan tık işleyicileri (Ctrl/⌘-tık) onClick'i korur ama aynı
// etikette rowKeyboard( ile klavye yarısını taşımak ZORUNDADIR — tarama
// yalnız onu muaf tutar.
function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.tsx$/.test(e) && !/\.test\.tsx$/.test(e)) out.push(p);
  }
  return out;
}
/* v0.10.933 (tablo standardı T2) — açılmayan satır rowActivation almaz
   (işaret imleç + hover verir): `{...(r.stmtHash ? rowActivation(…) : {})}`
   koşullu yayılımı da yardımcıyı benimsemiş sayılır. */
const ROW_ACTIVATION_SPREAD = /\{\.\.\.(?:\([^?{}()]*\?\s*)?rowActivation\(/;
describe('clickable rows use rowActivation (D3)', () => {
  const root = resolve(__dirname, '..');
  it('no raw <tr … onClick= outside the VirtualTable primitive', () => {
    const offenders: string[] = [];
    for (const f of walk(root)) {
      if (f.endsWith('components/ui/DataTable/VirtualTable.tsx')) continue;
      const src = readFileSync(f, 'utf8');
      const n = (src.match(/<tr[^>]*\sonClick=/g) ?? []).filter(tag => !tag.includes('rowKeyboard(')).length;
      if (n > 0) offenders.push(`${f.slice(root.length + 1)} (${n})`);
    }
    expect(offenders).toEqual([]);
  });
  // v0.10.925 — rowActivation'dan SONRA yazılan onKeyDown yayılımın
  // işleyicisini EZER ve hedef denetimini (`e.target !== e.currentTarget`)
  // kaybettirir: satır içindeki düğmeye basılan Enter/Boşluk satırı açıyordu
  // (ProblemsSection, AnomaliesPage, LogPatternsPanel ×2).
  it('rowActivation handler is not overridden by a later onKeyDown on the same tag', () => {
    const offenders: string[] = [];
    for (const f of walk(root)) {
      const src = readFileSync(f, 'utf8');
      for (const m of src.matchAll(new RegExp(ROW_ACTIVATION_SPREAD.source, 'g'))) {
        const i = m.index ?? 0;
        const rest = src.slice(i);
        const child = rest.search(/\n\s*<[A-Za-z]/); // açılış etiketinin sonu ≈ ilk çocuk eleman
        const tag = rest.slice(0, child < 0 ? 1200 : child);
        if (/\sonKeyDown=/.test(tag)) offenders.push(`${f.slice(root.length + 1)}:${src.slice(0, i).split('\n').length}`);
      }
    }
    expect(offenders).toEqual([]);
  });
  // v0.10.1105 — trace id taşıyan satır listelerinde id GERÇEK <Link> (orta /
  // Ctrl / ⌘ tık yeni sekme) ve tıkı satıra çıkmaz (paylaşılan stopRowClick);
  // satırın rowActivation / rowClickHandlers'ı düz tık + klavye için kalır.
  // Davranış: components/traces/traceIdRowLinks.render.test.tsx.
  it('trace-id row sites carry a real Link guarded by stopRowClick (v0.10.1105)', () => {
    for (const f of ['features/anomalies/ExceptionSampleRow.tsx', 'components/traces/ShapesView.tsx', 'pages/explore/RepeatsResult.tsx', 'pages/explore/TracesResult.tsx']) {
      const src = readFileSync(join(root, f), 'utf8');
      expect(src, f).toMatch(/<Link to=\{traceHref\(/);
      expect(src, f).toContain('onClick={stopRowClick}');
    }
  });
  it('the D3 sites adopted the helper', () => {
    // v0.10.1356 (R4) — Profiling ve servis OverviewTables gezinen satırdı:
    // rowActivation'dan rowClickHandlers + <Link>'e geçtiler (aşağıdaki R4 kapısı).
    // v0.10.1414 — Inbox, Hosts, Databases da (gezinen satır; aşağıdaki v0.10.1414
    // kapısı). Clusters listede KALIR: namespace / deployment satırları sayfa içi
    // süzgeç seçimi (aria-pressed), gezinme değil — rowActivation doğru.
    for (const f of ['components/DBQueriesPanel.tsx', 'features/anomalies/AnomaliesPage.tsx', 'pages/AIObservability.tsx', 'pages/AdminElastic.tsx', 'pages/service/ServicePodsTable.tsx', 'pages/Clusters.tsx', 'pages/SlowQueries.tsx', 'components/LogPatternsPanel.tsx']) {
      expect(readFileSync(join(root, f), 'utf8')).toMatch(ROW_ACTIVATION_SPREAD);
    }
    // v0.10.1865 — pages/Metrics.tsx düştü: Keşif tablosunun satırı gerçek bağlantı (getRowHref).
    for (const f of ['components/chart/StatsLegend.tsx', 'components/viz/TimeSeriesPanel.tsx']) {
      expect(readFileSync(join(root, f), 'utf8')).toContain('rowKeyboard(');
    }
  });
});

// v0.10.1356 (Web Interface Guidelines R4; tablo standardı T7 "gezinen satır
// gerçek bağlantı") — `rowActivation(() => navigate(X))` satırı role=button
// yapar ve HER tıkta aynı sekmede gezinir: Ctrl/⌘/orta tık / "yeni sekmede
// aç" / "bağlantıyı kopyala" ölüydü, ekran okuyucu bağlantı yerine düğme
// duyuruyordu (ARIA button'ın çocukları sunumsaldır — içteki <a> da gizlenir).
// Gezinen satırın deseni (explore/TracesResult, OperationsTable):
//   <tr {...rowClickHandlers(X, () => navigate(X))}>   // düz tık aynı sekme,
//     <td><Link to={X} onClick={stopRowClick}>…</Link></td>  // değiştiricili/orta tık yeni sekme
// Klavye durağı Link'in kendisi (Enter = tarayıcının bağlantı etkinleştirmesi);
// onOpen'lı tablonun j/k/↑/↓/Enter'ı useTableNav'da, değişmedi.
// Davranış: lib/rowLinkSites.render.test.tsx.
/** `src[open]` = '(' → eşleşen ')' indeksine kadar olan argüman listesi. */
function balancedArgs(src: string, open: number): string {
  let depth = 0;
  for (let i = open; i < src.length; i++) {
    const c = src[i];
    if (c === '(' || c === '{' || c === '[') depth++;
    else if (c === ')' || c === '}' || c === ']') { depth--; if (depth === 0) return src.slice(open + 1, i); }
  }
  return src.slice(open + 1);
}
/** Üst düzey ilk virgüle kadar olan argüman (iç içe parantezler atlanır). */
function firstArg(args: string): string {
  let depth = 0;
  for (let i = 0; i < args.length; i++) {
    const c = args[i];
    if (c === '(' || c === '{' || c === '[') depth++;
    else if (c === ')' || c === '}' || c === ']') depth--;
    else if (c === ',' && depth === 0) return args.slice(0, i).trim();
  }
  return args.trim();
}
const norm = (s: string) => s.replace(/\s+/g, ' ').trim();
/** Dosya `<Link … to={X} …>` çiziyor mu (X boşluk-normalize metin eşitliği). */
function rendersLinkTo(src: string, target: string): boolean {
  return jsxOpenTags(src, 'Link').some(t => norm(t.tag).includes(`to={${norm(target)}}`));
}
/** Gerekçe anahtarlı muafiyet — BOŞ kalmalı; eklenen her satır gerekçesini taşır. */
const ROW_NAV_ALLOW: Record<string, string> = {};

describe('navigating rows are real links (R4, v0.10.1356)', () => {
  const root = resolve(__dirname, '..');
  const files = walk(root).map(f => ({ rel: f.slice(root.length + 1), src: readFileSync(f, 'utf8') }));

  it('no rowActivation(() => navigate(…)) — the row would be a button that ignores Ctrl/⌘/middle click', () => {
    const offenders: string[] = [];
    for (const { rel, src } of files) {
      for (const m of src.matchAll(/rowActivation\(\s*\(\)\s*=>\s*navigate\(/g)) {
        const key = `${rel}:${src.slice(0, m.index ?? 0).split('\n').length}`;
        if (!(key in ROW_NAV_ALLOW)) offenders.push(key);
      }
    }
    expect(offenders).toEqual([]);
    expect(Object.keys(ROW_NAV_ALLOW)).toEqual([]);
  });

  it('rowClickHandlers(X, () => navigate(X)) and rowKeyboard(() => navigate(X)) rows render a <Link to={X}>', () => {
    const offenders: string[] = [];
    let checked = 0;
    for (const { rel, src } of files) {
      // Primitif kendi hücre linklerini dt.rowLink ile basar (getRowHref).
      if (rel === 'components/ui/DataTable/DataTable.tsx') continue;
      for (const m of src.matchAll(/\b(rowClickHandlers|rowKeyboard)\(/g)) {
        const at = (m.index ?? 0) + m[0].length - 1;
        const args = balancedArgs(src, at);
        let target: string | null = null;
        if (m[1] === 'rowClickHandlers') {
          const href = firstArg(args);
          const rest = args.slice(args.indexOf(href) + href.length).replace(/^\s*,\s*/, '');
          const nav = /^\(\)\s*=>\s*navigate\(/.exec(rest);
          if (!nav) continue; // gezinmeyi başka yardımcıya devreden satır (Services goToService) bu kapının dışında
          const navArgs = firstArg(balancedArgs(rest, nav[0].length - 1));
          if (norm(navArgs) !== norm(href)) offenders.push(`${rel}: rowClickHandlers href ≠ navigate hedefi (${href} / ${navArgs})`);
          target = href;
        } else {
          const nav = /^\s*\(\)\s*=>\s*navigate\(/.exec(args);
          if (!nav) continue;
          target = firstArg(balancedArgs(args, nav[0].length - 1));
        }
        checked++;
        if (!rendersLinkTo(src, target)) offenders.push(`${rel}: <Link to={${target}}> yok`);
      }
    }
    expect(offenders).toEqual([]);
    // R4'ün 6 sitesi + TracesResult + OperationsTable + Metrics (9) + v0.10.1414'ün
    // 8'i (TopEndpoints, DetailsEndpoints, Hosts, External, Databases ifade,
    // DependenciesTable, Clusters pod, Inbox). v0.10.1865 — Metrics katalog sitesi
    // düştü (Keşif tablosu DataTable getRowHref: gerçek bağlantı primitifte) → 16.
    expect(checked).toBeGreaterThanOrEqual(16);
  });

  it('the six R4 sites use rowClickHandlers + a Link guarded by stopRowClick', () => {
    for (const f of ['features/anomalies/ExceptionSampleRow.tsx', 'components/traces/ShapesView.tsx', 'pages/Profiling.tsx', 'pages/explore/RepeatsResult.tsx', 'pages/service/OverviewTables.tsx']) {
      const src = readFileSync(join(root, f), 'utf8');
      expect(src, f).toContain('rowClickHandlers(');
      expect(src, f).not.toMatch(/rowActivation\(/);
      expect(src, f).toContain('onClick={stopRowClick}');
    }
  });
});

// v0.10.1414 (tablo satırları gerçek link) — v0.10.1356'nın "bilinçli sınırı":
// SARMALAYICI fonksiyonla gezinen satırlar (`rowActivation(() => openPod(r))`,
// `onRowNavigate`, `window.location.assign`) yukarıdaki kapıya takılmıyordu —
// regex yalnız doğrudan `navigate(` görüyordu. Altı tablo aynı R4 desenine
// geçti; bu kapı geri dönüşü (sarmalayıcıyla yeniden role=button satır) yakalar.
// Davranış: lib/rowLinkSites1414.render.test.tsx.
const WRAPPED_NAV_SITES = [
  'pages/service/TopEndpointsCard.tsx',
  'pages/service/DetailsEndpointsSection.tsx',
  'pages/Hosts.tsx',
  'pages/External.tsx',
  'pages/Databases.tsx',
  'components/DependenciesTable.tsx',
  'pages/Clusters.tsx',
  'pages/Inbox.tsx',
];
/** Gezinen satırın sarmalayıcı adları (v0.10.1414 öncesi). */
const WRAPPED_NAV = /rowActivation\(\s*\(\)\s*=>\s*\(?\s*(?:\{\s*)?(?:window\.location|open(?:Pod|Host|Row|Stmt|DatabasePage)\b|gotoEp\b|onRowNavigate\b)/;

describe('wrapper-navigating rows are real links too (v0.10.1414)', () => {
  const root = resolve(__dirname, '..');
  it.each(WRAPPED_NAV_SITES)('%s: rowClickHandlers + <Link onClick={stopRowClick}>, sarmalayıcıyla rowActivation yok', (f) => {
    const src = readFileSync(join(root, f), 'utf8');
    expect(src).toContain('rowClickHandlers(');
    expect(src).toContain('onClick={stopRowClick}');
    expect(src).not.toMatch(WRAPPED_NAV);
    expect(src).not.toMatch(/window\.location\.assign\(/);
  });
  it('DependenciesTable gezinme kipinin anahtarı adres (rowHref) — onRowNavigate sarmalayıcısı yok', () => {
    for (const f of ['components/DependenciesTable.tsx', 'pages/Databases.tsx']) {
      expect(readFileSync(join(root, f), 'utf8'), f).not.toMatch(/onRowNavigate[=?:]/);
    }
  });
});
