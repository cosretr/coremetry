import { describe, it, expect } from 'vitest';
import {
  defaultPriority, fitColumnWidths, fitFloor, hiddenCellCss, pickFlexColumn,
  type FitColumnInput, type FitResult,
} from './dataTable';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.9.1030 regresyonu — operatör bildirimi: /inbox'ta Assignee tarafında
// tablo "bozuluyor", sayfa iframe gibi iç-kaydırmalı görünüyor.
//
// Kök: table-layout:fixed bir tablo, kolon genişliklerinin TOPLAMI
// width:100%'ü aşarsa büyür; is-fit kap masaüstünde overflow:visible
// (D2.1) olduğundan taşma #content'e çıkıp SAYFAYI yatay kaydırıyordu.
// Geniş monitörde sürüklenip px olarak kalıcılaşan düzen, dar laptopta
// garanti taşmaydı (v0.9.660 Users vakasının yapısal hâli).
//
// v0.10.1068 — operatör (prod, ~1440px laptop, Exceptions): "Kolonlar
// kayıyor, sığmıyor; sayfa responsive değil ve bu hemen hemen her tabloda
// böyle." Sözleşme genişledi (lib/dataTable.ts "BÜTÇELİ SIĞDIRMA"):
// a) sığan küme dokunulmaz, b) sürüklenmemişler oransal küçülür, c) sürüklenen
// (pinned) kolon da taşırmadan önce küçülür (v0.10.1057 REVİZYONU), d) tabanlar
// sığmazsa `priority`ye göre kolon gizlenir, e) yalnız öncelik-1 tabanlar bile
// sığmazsa taşma.

const c = (id: string, px: number | null, min = 48, extra: Partial<FitColumnInput> = {}): FitColumnInput =>
  ({ id, px, min, ...extra });
const p = (id: string, px: number, min = 48, extra: Partial<FitColumnInput> = {}): FitColumnInput =>
  ({ id, px, min, pinned: true, ...extra });
const fit = (cols: FitColumnInput[], fixed: number, box: number, reserve = 0) =>
  fitColumnWidths(cols, fixed, box, reserve) as FitResult;
/** Çizilen toplam: 'auto' (px null ya da sonuçtaki `auto`) kolon kalan alanı alır. */
const drawn = (cols: FitColumnInput[], out: FitResult, fixed: number, box: number) => {
  const shown = cols.filter(x => !out.hidden.includes(x.id));
  const fixedSum = shown.reduce((s, x) => {
    if (out.widths[x.id] != null) return s + out.widths[x.id];
    if (x.px == null || out.auto === x.id) return s;
    return s + x.px;
  }, 0);
  const autos = shown.filter(x => out.widths[x.id] == null && (x.px == null || out.auto === x.id));
  return { fixedSum, total: fixed + fixedSum + (autos.length ? Math.max(0, box - fixed - fixedSum) : 0), autos };
};

describe('fitColumnWidths (v0.9.1030 → v0.10.1068)', () => {
  it('ölçüm yok (containerPx ≤ 0) → null — fail-open', () => {
    expect(fitColumnWidths([c('a', 5000)], 0, 0)).toBeNull();
    expect(fitColumnWidths([c('a', 5000)], 0, -1)).toBeNull();
  });

  it.each([
    ['sığan küme', [c('a', 200), c('b', 300)], 34, 600],
    ['tam sınır', [c('a', 200), c('b', 366)], 34, 600],
    ['flex tabanı rezervli ve sığıyor', [c('a', 300), c('t', null, 200)], 0, 500],
  ] as const)('a — %s: dokunulmaz (boş sonuç)', (_n, cols, fixed, box) => {
    expect(fit([...cols], fixed, box)).toEqual({ widths: {}, hidden: [], auto: null });
  });

  it('b — taşan küme oransal küçülür, oranlar korunur, toplam kaba sığar', () => {
    const cols = [c('prio', 160), c('svc', 380), c('assignee', 660)];
    const out = fit(cols, 34, 634);
    expect(34 + out.widths.prio + out.widths.svc + out.widths.assignee).toBeLessThanOrEqual(634);
    expect(out.widths.svc / out.widths.prio).toBeCloseTo(380 / 160, 1);
    expect(out.widths.assignee / out.widths.svc).toBeCloseTo(660 / 380, 1);
  });

  it('b — taban kilitlenir, kalan pay kilitsizlere yeniden oranlanır', () => {
    const out = fit([c('a', 800, 48), c('b', 100, 90)], 0, 450);
    expect(out.widths.b).toBe(90);
    expect(out.widths.a).toBeGreaterThan(300);
    expect(out.widths.a + out.widths.b).toBeLessThanOrEqual(450);
  });

  it("b — flex ('auto') kolon tabanını korur, diğerleri ona yer açar", () => {
    const cols = [c('fixed', 500), c('detail', null, 100)];
    const out = fit(cols, 34, 600);
    expect(out.widths.detail).toBeUndefined();
    expect(out.widths.fixed).toBeLessThanOrEqual(600 - 34 - 100);
    expect(drawn(cols, out, 34, 600).total).toBeLessThanOrEqual(600);
  });

  // (a) — flex beyan etmemiş tablo: seçilmiş metin kolonu açığı ÖNCE emer
  // (tabanına kadar), eşikte süreklidir.
  it.each([
    // [kap, beklenen 'auto' genişliği]  — beyan: a 200 + text 300 + b 200 = 700
    [700, null],  // sığıyor: text beyanında (auto yok)
    [699, 299],   // eşik: text tam açığı emer, diğerleri aynen
    [600, 200],
    [380, 180],   // text tabanda (180), diğerleri küçülür
  ])('a — seçilmiş metin kolonu kap %i px → auto %s', (box, autoW) => {
    const cols = [c('a', 200, 100), c('text', 300, 180, { flex: true }), c('b', 200, 100)];
    const out = fit(cols, 0, box);
    if (autoW == null) { expect(out.auto).toBeNull(); return; }
    expect(out.auto).toBe('text');
    const d = drawn(cols, out, 0, box);
    expect(box - d.fixedSum).toBe(autoW);
  });

  // v0.10.1057 → v0.10.1068 REVİZYONU (operatör: taşma kötü deneyim).
  it('c — pinned SIĞDIĞI sürece aynen; önce sürüklenmemişler küçülür', () => {
    const out = fit([p('a', 300), c('b', 300), c('c', 300)], 0, 600);
    expect(out.widths.a).toBeUndefined(); // kalıcı px'inde (300) çizilir
    expect(out.widths.b + out.widths.c).toBeLessThanOrEqual(300);
    expect(fit([p('a', 100), c('b', 100)], 0, 600)).toEqual({ widths: {}, hidden: [], auto: null });
  });

  it('c — 1057 vakası (Argo, 1100px pencere, kap 400): pinned artık TAŞIRMAZ, tabana kadar küçülür', () => {
    // Eskiden { name: 180, health: 190, repo: 140 } = 510 > 400 → taşma.
    const cols = [c('name', 260, 180), p('health', 190, 90), c('repo', 240, 140)];
    const out = fit(cols, 0, 400);
    // Tabanlar 410 > 400: öncelik verilmemiş (1) → gizleme yok, herkes tabanda (e).
    expect(out.hidden).toEqual([]);
    expect(out.widths).toEqual({ name: 180, health: 90, repo: 140 });
    // Kap 450: sürüklenmemişler tabanda, pinned kalan 130'a küçülür (190 değil).
    const out2 = fit(cols, 0, 450);
    expect(out2.widths).toEqual({ name: 180, health: 130, repo: 140 });
    expect(out2.widths.name + out2.widths.health + out2.widths.repo).toBeLessThanOrEqual(450);
  });

  it('c — birden çok pinned oransal küçülür, kendi tabanında kilitlenir', () => {
    const out = fit([p('a', 400, 100), p('b', 200, 150), c('c', 200, 100)], 0, 450);
    expect(out.widths.c).toBe(100);
    expect(out.widths.b).toBe(150);
    expect(out.widths.a).toBe(200);
  });

  // GitOps Argo uygulamaları (v0.10.1057 ölçümü): beyan 1650 px, kap 1178 px.
  const ARGO: FitColumnInput[] = [
    c('name', 260, 180, { flex: true }), c('sync', 110, 90), c('health', 110, 90),
    c('auto', 96, 90, { priority: 3 }), c('syncs', 170, 120), c('dest', 200, 140),
    c('match', 110, 90, { priority: 3 }), c('workloads', 200, 120),
    c('instance', 160, 110, { priority: 3 }), c('repo', 240, 140, { priority: 3 }),
  ];
  it('GitOps 1650/1178 — tabanlar (1170) sığar: gizleme yok, kaba tam sığar', () => {
    expect(ARGO.reduce((s, x) => s + (x.px ?? 0), 0)).toBe(1656);
    const out = fit(ARGO, 0, 1178, 76);
    expect(out.hidden).toEqual([]);
    expect(out.auto).toBe('name');
    expect(drawn(ARGO, out, 0, 1178).total).toBeLessThanOrEqual(1178);
    for (const x of ARGO) if (out.widths[x.id] != null) expect(out.widths[x.id]).toBeGreaterThanOrEqual(x.min);
  });

  it('GitOps 1150 px kap — öncelik-3 kolonlar sağdan gizlenir, "+N" payı son kolona', () => {
    const out = fit(ARGO, 0, 1150, 76);
    expect(out.hidden).toEqual(['repo']);
    // Gizleme sonrası: tabanlar 1030 + pay 76 = 1106 > 1100 → bir tane daha.
    const out2 = fit(ARGO, 0, 1100, 76);
    expect(out2.hidden).toEqual(['repo', 'instance']);
    const last = ARGO.filter(x => !out2.hidden.includes(x.id)).at(-1)!;
    expect(out2.widths[last.id]).toBeGreaterThanOrEqual(last.min + 76);
    expect(drawn(ARGO, out2, 0, 1100).total).toBeLessThanOrEqual(1100);
  });

  it.each([
    // [kap, beklenen gizlenenler] — Exceptions şekli (admin, 24 px leading)
    [1400, []],
    [1230, ['firstSeen']],
    [1100, ['firstSeen', 'assignee']],
    [980, ['firstSeen', 'assignee', 'lastSeen']],
    [840, ['firstSeen', 'assignee', 'lastSeen', 'occ']],
    [500, ['firstSeen', 'assignee', 'lastSeen', 'occ']], // e — yalnız öncelik-1 kaldı: taşar
  ])('d — öncelik sırası (eşitlikte sağdaki önce), kap %i → %j', (box, hidden) => {
    const cols = [
      c('prio', 68, 64, { priority: 1 }), c('state', 140, 140, { priority: 1 }),
      c('type', null, 200, { priority: 1 }), c('service', 170, 120, { priority: 1 }),
      c('occ', 100, 92, { priority: 2 }), c('firstSeen', 136, 136, { priority: 4 }),
      c('lastSeen', 136, 136, { priority: 3 }), c('assignee', 150, 120, { priority: 3 }),
      c('actions', 208, 208, { priority: 1 }),
    ];
    const out = fit(cols, 24, box);
    expect(out.hidden).toEqual(hidden);
    if (box >= 840) expect(drawn(cols, out, 24, box).total).toBeLessThanOrEqual(box);
  });

  it('d — operatörün geri açtığı (forceShow) kolon gizlenmez; yerine sıradaki düşer', () => {
    const cols = [c('a', 200, 200), c('b', 150, 150, { priority: 3 }), c('c', 150, 150, { priority: 2 })];
    expect(fit(cols, 0, 400).hidden).toEqual(['b']);
    const forced = cols.map(x => (x.id === 'b' ? { ...x, forceShow: true } : x));
    expect(fit(forced, 0, 400).hidden).toEqual(['c']);
    // Hiç aday kalmazsa gizleme yok — operatörün açık seçimi, tablo kaydırır.
    const all = cols.map(x => ({ ...x, forceShow: true }));
    expect(fit(all, 0, 400).hidden).toEqual([]);
  });

  it("iki 'auto' kolon: kalan alan tabanlarla orantılı (eşit bölünüp büyük taban ezilmez)", () => {
    // Clusters pod şekli: Namespace (110) + Pod (180) esner; kalan 290.
    const cols = [c('ns', null, 110), c('pod', null, 180), c('cpu', 210, 72)];
    const out = fit(cols, 0, 500);
    expect(out.widths.ns).toBe(110);           // 290 × 110/290
    expect(out.widths.pod).toBeUndefined();    // son 'auto' artığı alır (180)
    // Geniş kapta da orantılı: kalan 790 → ns ≈ 299, pod ≈ 491.
    const wide = fit(cols, 0, 1000);
    expect(wide.widths.ns).toBe(Math.floor(790 * 110 / 290));
  });

  it('genişlikler tamsayı (colgroup px değerleri)', () => {
    const out = fit([c('a', 333), c('b', 334)], 0, 500);
    for (const v of Object.values(out.widths)) expect(Number.isInteger(v)).toBe(true);
  });
});

describe('fitFloor / pickFlexColumn / defaultPriority (v0.10.1068)', () => {
  it.each([
    [{ width: 168, minWidth: 150 }, 150],
    [{ width: 168 }, 101],              // %60
    [{ width: 60 }, 48],                // en az 48
    [{ width: 40 }, 40],                // en çok beyan
    [{ flex: true }, 160],              // esneyen kolon varsayılanı
    [{ flex: true, minWidth: 220 }, 220],
    [{}, 72],                           // DEFAULT_W 120 → %60
  ])('fitFloor(%j) = %i', (col, want) => {
    expect(fitFloor(col, 120)).toBe(want);
  });

  it('pickFlexColumn: beyan edilen flex kazanır; yoksa en geniş METİN kolonu', () => {
    expect(pickFlexColumn([{ id: 'a', label: 'A', width: 400 }, { id: 'b', label: 'B', flex: true }], 120)).toBe('b');
    expect(pickFlexColumn([
      { id: 'n', label: 'N', width: 500, numeric: true },
      { id: 'x', label: 'X', width: 260, kind: 'actions' },
      { id: 'h', label: 'H', width: 900, headerHidden: true },
      { id: 's', label: 'S', width: 180 },
      { id: 't', label: 'T', width: 240 },
    ], 120)).toBe('t');
    expect(pickFlexColumn([{ id: 'n', label: 'N', numeric: true }], 120)).toBeNull();
  });

  it('defaultPriority: beyan > ilk / esneyen / eylem = 1 > diğerleri 2', () => {
    expect(defaultPriority({ priority: 4 }, true, true)).toBe(4);
    expect(defaultPriority({}, true, false)).toBe(1);
    expect(defaultPriority({}, false, true)).toBe(1);
    expect(defaultPriority({ kind: 'actions' }, false, false)).toBe(1);
    expect(defaultPriority({}, false, false)).toBe(2);
  });
});

describe('hiddenCellCss (v0.10.1068)', () => {
  it('gizli yok → boş', () => {
    expect(hiddenCellCss('f1', 1, 5, ['a', 'b', 'c'], new Set())).toBe('');
  });
  it('tam hücre sayılı satırı hedefler (leading dahil sıra + nth-last eşi)', () => {
    const css = hiddenCellCss('f1', 1, 5, ['a', 'b', 'c', 'd'], new Set(['c']));
    // leading 1 + c (3.) → 4. hücre; 5 hücreli satırda sondan 2.
    expect(css).toContain('table:has(> colgroup[data-dt-fit="f1"]) > tbody > tr > td:nth-child(4):nth-last-child(2)');
    expect(css).toContain('> thead > tr > th:nth-child(4):nth-last-child(2)');
    expect(css.trim().endsWith('{ display: none; }')).toBe(true);
  });
});

// ── ULAŞILABİLİRLİK — v0.9.1334 ─────────────────────────────────────────
//
// Saf testler `fitColumnWidths`in DOĞRU HESAPLADIĞINI çiviliyor; bunlar onun
// ÇAĞRILDIĞINI. v0.9.1078'den 2026-08-24'e kadar bir muhafaza (computed-style
// overflowX) sığdırmayı sessizce kapatıyordu ve testler yeşil kaldı.
// v0.10.1068 — ölçüm DataTableColgroup'ta (kabın clientWidth'i hook'a
// raporlanır), hesap hook'ta (useMemo içinde `return fitColumnWidths(`).
describe('fitColumnWidths ULAŞILABİLİR (v0.9.1334)', () => {
  const code = () => {
    const src = readFileSync(
      resolve(__dirname, '..', 'components', 'ui', 'DataTable', 'DataTable.tsx'), 'utf8');
    // Yorumları soy: şerhler tarihi anlatmak için bu kelimeleri KULLANIYOR.
    return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
  };
  it('ölçüm overflow/computed-style muhafazasına BAĞLI DEĞİL ve kabın genişliğini alıyor', () => {
    const src = code();
    expect(src).not.toContain('overflowX');
    expect(src).not.toContain('getComputedStyle');
    expect(src).toContain('reportLayout(wrap.clientWidth');
  });

  // ⚠ Varlık ≠ kullanım: `return null && fitColumnWidths(...)` dizgiyi
  // taşır ama çağırmaz. Yüklem ŞEKLE bakar.
  it('çağrı kısa-devreye alınmamış (return doğrudan fitColumnWidths)', () => {
    expect(code()).toMatch(/return\s+fitColumnWidths\(/);
  });
});
