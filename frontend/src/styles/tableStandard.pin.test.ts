// tableStandard.pin — v0.10.933 (tablo standardı dilim 1: tek CSS sürümü).
//
// Operatör onayı: docs/DECISIONS.md "Tablo standardı" (T1–T12). Dilim 1
// yalnız CSS + birkaç TSX işareti; burada çivilenen kararlar hiçbir
// tsc/eslint/colorLeaks kapısına takılmaz — `tbody tr { cursor: pointer }`
// satırını geri yazmak tamamen geçerli CSS'tir ve ekranda "bozuk" bir şey
// göstermez, yalnız 150 tıklanmaz satır yine tıklanır görünür.
//   T2  imleç + hover yalnız tıklanabilir satırda; tek hover tonu --bg2
//       (bg2 zeminli iç kutuda — `td.row-detail`, log detay KV satırı — bir
//       kademe üst --bg3, yoksa hover görünmez).
//   T3  sessiz başlık: boştaki ↕ gizli, sıralı etiket accent değil, tek
//       başlık yüzeyi, başlık tek satır + "…".
//   T4/T5  tek yazı boyu (tablonunki), sayılar arayüz fontunda, TEK
//       monospace yığını (--font-mono).
//   T10 tek çerçeve: yüzey içindeki `.table-wrap` kenarlık/köşe çizmez
//       (istisna: tonlu AI cevap kartındaki markdown tablosu çerçeveli).
// (T6 `--row-h` ↔ ROW_H eşitliği: ui/DataTable/rowHeight.test.ts.)
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const CSS = readFileSync(resolve(__dirname, 'globals.css'), 'utf8');
// Yorumları BOŞALT (paletteStep2 deseni): gerekçe yorumlarında
// `tbody tr { cursor: pointer }` gibi eski kurallar anılıyor.
const CLEAN = CSS.replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '));

interface Rule { selectors: string[]; body: string }
const RULES: Rule[] = [...CLEAN.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(m => ({
  selectors: m[1].split(',').map(s => s.trim().replace(/\s+/g, ' ')),
  body: m[2],
}));
const rulesFor = (sel: string) => RULES.filter(r => r.selectors.includes(sel));
const indexOf = (sel: string): number => RULES.findIndex(r => r.selectors.includes(sel));
const bodyOf = (sel: string): string => {
  const r = rulesFor(sel)[0];
  if (!r) throw new Error(`kural yok: ${sel}`);
  return r.body;
};
const opacity = (body: string) => Number(/opacity:\s*([\d.]+)/.exec(body)?.[1]);

const INTERACTIVE = ['tbody tr[role="button"]', 'tbody tr[data-row-action]', 'tbody tr:has(> td > .row-link)'];

describe('T2 — imleç ve hover yalnız tıklanabilir satırda', () => {
  it('taban `tbody tr` imleç taşımıyor; çıplak `tbody tr:hover` kuralı yok', () => {
    expect(bodyOf('tbody tr')).not.toMatch(/cursor\s*:/);
    expect(rulesFor('tbody tr:hover'), 'her satıra hover geri geldi').toEqual([]);
    expect(rulesFor('tbody tr:hover td.sticky-left')).toEqual([]);
    expect(rulesFor('tbody tr:hover td.sticky-right')).toEqual([]);
  });

  it('pointer + bg2 hover üç işaretin HER birinde', () => {
    for (const sel of INTERACTIVE) {
      expect(bodyOf(sel), sel).toMatch(/cursor:\s*pointer/);
      expect(bodyOf(`${sel}:hover`), `${sel}:hover`).toMatch(/background:\s*var\(--bg2\)/);
      expect(bodyOf(`${sel}:hover td.sticky-left`)).toMatch(/background:\s*var\(--bg2\)/);
      expect(bodyOf(`${sel}:hover td.sticky-right`)).toMatch(/background:\s*var\(--bg2\)/);
    }
  });

  it('tek hover tonu: satır hover\'larında accent karışımı yok', () => {
    // Log kalıbı satırının kendi hover'ı yok: tıklanabilir olan role=button ile ortak kuraldan alır.
    expect(CLEAN).not.toMatch(/(^|\n)\.lp-row:hover td\s*\{/);
    /* v0.10.933 (tablo standardı T2) — KV satırı bg2 zeminli log detay hücresinde: bir kademe üst. */
    expect(bodyOf('.kv-filterable:hover td')).toMatch(/background:\s*var\(--bg3\)/);
    /* v0.10.933 (tablo standardı T2) — TraceFacetsTab satırı tıklanmaz: hover yok (sınıf test kancası). */
    expect(rulesFor('.tf-row:hover td'), '.tf-row hover geri geldi').toEqual([]);
    const accentRowHover = RULES
      .filter(r => r.selectors.some(s => /\btr\b[^,]*:hover|-row:hover td/.test(s)))
      .filter(r => /color-mix\(in srgb, var\(--accent\)/.test(r.body))
      .map(r => r.selectors.join(', '));
    expect(accentRowHover).toEqual([]);
  });

  it('detay hücresine gömülü tıklanabilir satır bg3 (bg2 iç kutuda görünür kalır)', () => {
    for (const sel of INTERACTIVE) {
      expect(bodyOf(`td.row-detail ${sel}:hover > td`), sel).toMatch(/background:\s*var\(--bg3\)/);
    }
  });

  it('seçili hâl tek: `.row-selected` --accent-bg', () => {
    expect(bodyOf('.row-selected')).toMatch(/background:\s*var\(--accent-bg\)/);
  });
});

describe('T3 — sessiz başlık', () => {
  it('boştaki sıralama glifi gizli; hover/odakta belirir; sıralı kolonda tam', () => {
    expect(opacity(bodyOf('thead th .sort-arrow'))).toBe(0);
    expect(opacity(bodyOf('thead th:hover .sort-arrow'))).toBeGreaterThan(0);
    expect(rulesFor('thead th:hover .sort-arrow')[0].selectors).toContain('thead th:focus-visible .sort-arrow');
    expect(opacity(bodyOf('thead th.sorted .sort-arrow'))).toBe(1);
  });

  it('dokunmatikte (hover: none) ipucu soluk görünür', () => {
    const m = /@media \(hover: none\) \{\s*thead th \.sort-arrow \{([^}]*)\}/.exec(CLEAN);
    expect(m, '(hover: none) sort-arrow kuralı yok').toBeTruthy();
    const o = opacity(m![1]);
    expect(o).toBeGreaterThan(0);
    expect(o).toBeLessThan(1);
  });

  it('sıralı etiket accent değil (--text)', () => {
    const body = bodyOf('thead th.sorted');
    expect(body).toMatch(/color:\s*var\(--text\)/);
    expect(body).not.toMatch(/--accent/);
  });

  it('tek başlık yüzeyi: `.vt-scroll thead th` == `.table-wrap.is-scroll thead th`', () => {
    const bg = (sel: string) => /background:\s*([^;]+)/.exec(bodyOf(sel))?.[1].trim();
    expect(bg('.vt-scroll thead th')).toBe(bg('.table-wrap.is-scroll thead th'));
    expect(bg('.vt-scroll thead th')).toBe('var(--bg1)');
  });

  it('taban `thead th` tek satır + "…" (ve resize çapası relative kalır)', () => {
    const body = bodyOf('thead th');
    expect(body).toMatch(/white-space:\s*nowrap/);
    expect(body).toMatch(/overflow:\s*hidden/);
    expect(body).toMatch(/text-overflow:\s*ellipsis/);
    expect(body).toMatch(/position:\s*relative/);
  });
});

describe('T4/T5 — tek yazı boyu, sayılar arayüz fontunda, tek mono yığını', () => {
  it('tablo boyu token\'dan; tablo içindeki .mono boyutu miras alır', () => {
    expect(bodyOf('table')).toMatch(/font-size:\s*var\(--fs-sm\)/);
    /* v0.10.933 (tablo standardı T5) — `:where(table)` özgüllüğü 0,1,0: taban
       `code, pre, .mono { 12px }`'i sırayla yener, sonraki tek sınıflı boyut
       kuralları (.ib-when, .badge, …) yine kazanır. `table .mono` (0,1,1) geri
       gelirse hepsini ezer. */
    expect(bodyOf(':where(table) .mono')).toMatch(/font-size:\s*inherit/);
    expect(rulesFor('table .mono'), '`table .mono` (0,1,1) tek sınıflı boyutları ezer').toEqual([]);
    expect(bodyOf('.mono')).toMatch(/font-size:\s*12px/);
    expect(indexOf(':where(table) .mono'), 'taban `.mono` 12px\'i sırayla yenmeli').toBeGreaterThan(indexOf('.mono'));
    for (const sel of ['.ib-when', '.badge', '.trend-spark__tt', '.mtp-id-dim', '.mtp-id-name', '.field-hint']) {
      expect(indexOf(sel), `${sel} :where kuralından SONRA gelmeli`).toBeGreaterThan(indexOf(':where(table) .mono'));
      expect(bodyOf(sel), sel).toMatch(/font-size:/);
    }
  });

  it('`td.num.mono` / `th.num.mono` monospace\'ten çıkar (hiza tabular-nums\'ta)', () => {
    const r = rulesFor('td.num.mono')[0];
    expect(r, 'td.num.mono kuralı yok').toBeDefined();
    expect(r.selectors).toContain('th.num.mono');
    expect(r.body).toMatch(/font-family:\s*inherit/);
    expect(r.body).toMatch(/font-size:\s*inherit/);
    expect(bodyOf('td.num')).toMatch(/font-variant-numeric:\s*tabular-nums/);
  });

  it('TEK monospace yığını: yalnız --font-mono token\'ı "monospace" yazar', () => {
    const hits = [...CLEAN.matchAll(/[^\n;{}]*monospace[^\n;{}]*/g)].map(m => m[0].trim());
    expect(hits).toEqual(['--font-mono: ui-monospace, SFMono-Regular, Menlo, monospace']);
    expect(bodyOf('.mono')).toMatch(/font-family:\s*var\(--font-mono\)/);
    expect(bodyOf('.logtbl-dense > tbody > tr > td')).toMatch(/font-family:\s*var\(--font-mono\)/);
  });
});

describe('T10 — tek çerçeve', () => {
  const HOSTS = ['.card', '.card-static', '.ov-card-b', '.drawer-panel', '.drawer-section', '.modal-dialog'];

  it.each(HOSTS)('%s içindeki .table-wrap kenarlık/köşe çizmez, zemini ellemez', host => {
    const body = bodyOf(`${host} .table-wrap`);
    expect(body).toMatch(/border:\s*0/);
    expect(body).toMatch(/border-radius:\s*0/);
    expect(body, 'zemin bg1 kalmalı (sticky başlık/kolon opaklığı)').not.toMatch(/background/);
  });

  it('istisna: AI cevap kartındaki markdown tablosu çerçeveli (de-frame listesinden SONRA)', () => {
    /* v0.10.933 (tablo standardı T10) — tonlu kart yüzey değil; aynı özgüllük, sıra kazanır. */
    const body = bodyOf('.ai-answer-card .table-wrap');
    expect(body).toMatch(/border:\s*1px solid var\(--border\)/);
    expect(body).toMatch(/border-radius:\s*var\(--radius-sm\)/);
    for (const host of HOSTS) {
      expect(indexOf('.ai-answer-card .table-wrap'), host).toBeGreaterThan(indexOf(`${host} .table-wrap`));
    }
  });

  it('sayfadaki .table-wrap çerçevesini korur', () => {
    const body = bodyOf('.table-wrap');
    expect(body).toMatch(/border:\s*1px solid var\(--border\)/);
    expect(body).toMatch(/border-radius:\s*var\(--radius\)/);
  });

  it('DrawerSection kancayı basar', () => {
    const src = readFileSync(resolve(__dirname, '../components/ui/Drawer.tsx'), 'utf8');
    expect(src).toContain('<div className="drawer-section"');
  });
});
