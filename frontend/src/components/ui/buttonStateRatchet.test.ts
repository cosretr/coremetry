// buttonStateRatchet — v0.10.1318 (K3): açık/kapalı DURUM birincil dolgu değil.
//
// Ne çiviliyor: `variant={on ? 'primary' : 'secondary'}` deseni — bir
// anahtarın açık hâlini birincilin DOLGUSUYLA söylemek. Bu "durum" ile
// "asıl eylem"i aynı dille anlatıyor ve grup başına tek birincil kuralını
// (K4) sessizce bozuyordu: Logs araç çubuğunda "◆ With trace" açıkken
// Search'ün yanında ikinci bir dolu mavi oluyordu. Doğru dil Button
// `active` (→ `.is-active` ikincil tint'i + aria-pressed); seçenekler
// arasında tek seçim (radyo anlamı) ise SegmentedControl.
//
// Neden kapı: `tsc` için iki yazım da kusursuz, eslint ifadeye bakmaz,
// jsdom rengi hesaplamaz. Kalan siteler bir TAVAN'dı (aşağı iner, yukarı
// çıkmaz); v0.10.1342 (dilim B) son 10 siteyi göçürdü → tavan 0.
//   (a) koşullu birincil sayısı ≤ TAVAN (= 0),
//   (b) göç eden iki dosya (Logs, ProblemVerdictActions) `active=` taşıyor
//       ve koşullu birincile geri dönmedi,
//   (c) hiçbir <Button> hem `active=` hem dolu varyant (primary|danger) taşımaz,
//   (d) v0.10.1342 dilim B siteleri seçtikleri kalıpta kalıyor (active /
//       SegmentedControl / vurgu tablosu) — biri üçlüye dönerse adıyla düşer.
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { jsxOpenTags } from '@/styles/jsxTags';

const SRC = resolve(__dirname, '..', '..');
const TEST_MARK = '.' + 'test' + '.';

// styles/zLayers.test.ts `stripTsComments` ile aynı durum makinesi (oradan
// içe aktarmak o dosyanın describe'larını da koştururdu). Yorumları boşluğa
// çevirir, satırları korur; dizgi içindeki `//` yorum sanılmaz.
function stripTsComments(src: string): string {
  let out = '';
  let mode: 'code' | 'line' | 'block' | '"' | "'" | '`' = 'code';
  for (let i = 0; i < src.length; i++) {
    const c = src[i];
    const n = src[i + 1] ?? '';
    if (mode === 'code') {
      if (c === '/' && n === '/') { mode = 'line'; out += '  '; i++; continue; }
      if (c === '/' && n === '*') { mode = 'block'; out += '  '; i++; continue; }
      if (c === '"' || c === "'" || c === '`') { mode = c; out += c; continue; }
      out += c;
    } else if (mode === 'line') {
      if (c === '\n') { mode = 'code'; out += '\n'; } else out += ' ';
    } else if (mode === 'block') {
      if (c === '*' && n === '/') { mode = 'code'; out += '  '; i++; continue; }
      out += c === '\n' ? '\n' : ' ';
    } else {
      if (c === '\\') { out += '  '; i++; continue; }
      if (c === mode) mode = 'code';
      out += c;
    }
  }
  return out;
}

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (e.name === 'node_modules') continue;
    const p = join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (e.name.endsWith('.tsx') && !e.name.includes(TEST_MARK)) out.push(p);
  }
  return out;
}

const FILES = walk(SRC).map(p => ({ rel: p.slice(SRC.length + 1), src: stripTsComments(readFileSync(p, 'utf8')) }));
const COND_PRIMARY = /variant=\{[^}]*\?\s*['"]primary['"]/g;

// v0.10.1318 ölçümü: 10 site (MethodHotspots, FilterGroupBuilder,
// LogPatternsPanel, LogFieldsPanel, AIExplainButton, AdminClickhouse ×2,
// Alerts, Metrics, Explore). v0.10.1342 (dilim B) hepsini göçürdü:
//   durum anahtarı / komut  → `variant="secondary" active={x}`
//       (LogPatternsPanel Trend, LogFieldsPanel ⚠ ayıran, Metrics editör,
//        Explore Repeats preset'leri),
//   seçenekler arası tek seçim → SegmentedControl
//       (MethodHotspots tür, FilterGroupBuilder AND|OR, Alerts kural türü),
//   bağlamsal vurgu (durum değil) → arama tablosu
//       (AIExplainButton EMPHASIS_LOOK, AdminClickhouse CONFIRM_VARIANT).
// Tavan 0: yeni bir koşullu birincil eklemek bu testi düşürür.
const CEILING = 0;

describe('K3 — durum birincil dolgu değil (v0.10.1318)', () => {
  it('tarama boşa dönmüyor', () => {
    expect(FILES.length).toBeGreaterThan(100);
    expect(FILES.some(f => f.rel === 'pages/Logs.tsx')).toBe(true);
  });

  it(`(a) koşullu birincil sayısı ≤ ${CEILING} (tavan yalnız aşağı iner)`, () => {
    const hits: string[] = [];
    for (const f of FILES) {
      for (const m of f.src.matchAll(COND_PRIMARY)) {
        hits.push(`${f.rel}:${f.src.slice(0, m.index).split('\n').length}`);
      }
    }
    expect(hits.length, `Koşullu birincil (durumu dolguyla söylüyor) — Button \`active\` kullan:\n${hits.join('\n')}`)
      .toBeLessThanOrEqual(CEILING);
  });

  it('(b) Logs ve ProblemVerdictActions `active` ile — koşullu birincile dönmedi', () => {
    // v0.10.1502 — /logs'un son anahtarı (⤶ sar) da tablo araç satırına
    // taşındı; Logs.tsx'te Button kalmadı ama koşullu birincil de yok.
    for (const rel of ['pages/Logs.tsx', 'components/ProblemVerdictActions.tsx']) {
      const f = FILES.find(x => x.rel === rel);
      expect(f, rel).toBeDefined();
      if (rel !== 'pages/Logs.tsx') expect(f!.src, rel).toContain('active=');
      expect([...f!.src.matchAll(COND_PRIMARY)].length, rel).toBe(0);
    }
    // v0.10.1498 — /logs anahtarları saf sunum bileşenlerine taşındı:
    // ◆ With trace → + Filter ▾ (LogsActionsMenus), Live → LogsQueryBar.
    // İkisi de `active` ile, koşullu birincil yok.
    for (const [rel, needle] of [
      // v0.10.1830 — "⤶ sar" Görünüm ▾'e katıldı (gövde satır yüksekliği);
      // araç satırı shadcn, anahtar kalmadı — koşullu birincil de yok.
      ['pages/logs/LogsTableToolbar.tsx', 'p.onToggleAsc'],
      ['pages/logs/LogsActionsMenus.tsx', 'active={p.hasTrace}'],
      ['pages/logs/LogsQueryBar.tsx', 'pressed={p.live}'], // v0.10.1821 — shadcn Toggle
    ] as const) {
      const f = FILES.find(x => x.rel === rel);
      expect(f, rel).toBeDefined();
      expect(f!.src, rel).toContain(needle);
      expect([...f!.src.matchAll(COND_PRIMARY)].length, rel).toBe(0);
    }
    const pv = FILES.find(x => x.rel === 'components/ProblemVerdictActions.tsx')!.src;
    expect(pv).toContain("active={current?.verdict === 'real'}");
    expect(pv).toContain("active={current?.verdict === 'noise'}");
  });

  it('(d) v0.10.1342 dilim B siteleri seçtikleri kalıpta kalıyor', () => {
    const src = (rel: string) => {
      const f = FILES.find(x => x.rel === rel);
      expect(f, rel).toBeDefined();
      expect([...f!.src.matchAll(COND_PRIMARY)].length, rel).toBe(0);
      return f!.src;
    };
    // Durum anahtarı / komut → ikincil + active.
    expect(src('components/LogPatternsPanel.tsx')).toContain('active={trend}');
    // v0.10.1853 — LogFieldsPanel silindi; ⚠ ayıran coremetry/logs/FieldSidebar'da shadcn Button + aria-pressed.
    expect(src('pages/Metrics.tsx')).toContain('active={editor}');
    expect(src('pages/Explore.tsx')).toContain('active={active}');
    // Tek seçim → SegmentedControl.
    for (const rel of ['components/MethodHotspots.tsx', 'components/FilterGroupBuilder.tsx', 'pages/Alerts.tsx']) {
      expect(src(rel), rel).toContain('<SegmentedControl');
    }
    // Vurgu → arama tablosu (üçlü değil).
    expect(src('components/ai/AIExplainButton.tsx')).toContain('variant={look.variant}');
    const ch = src('pages/AdminClickhouse.tsx');
    expect(ch).toContain("variant={CONFIRM_VARIANT[confirmKind ?? 'rollback']}");
    expect(ch).toContain('variant={CONFIRM_VARIANT[confirmKind]}');
  });

  it('(c) hiçbir <Button> `active` ile dolu varyant (primary|danger) taşımaz', () => {
    const bad: string[] = [];
    let scanned = 0;
    for (const f of FILES) {
      for (const t of jsxOpenTags(f.src, 'Button')) {
        if (!/\sactive(?=[\s=/>])/.test(t.tag)) continue;
        scanned++;
        if (/variant=["'{]+['"]?(primary|danger)['"]/.test(t.tag)) bad.push(`${f.rel}:${t.line}`);
      }
    }
    expect(scanned, 'active taşıyan Button bulunamadı — tarama kaymış').toBeGreaterThan(5);
    expect(bad, `active + dolu varyant (durum ikincilin tint'idir):\n${bad.join('\n')}`).toEqual([]);
  });
});
