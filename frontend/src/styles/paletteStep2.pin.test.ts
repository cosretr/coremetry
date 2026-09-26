// paletteStep2.pin — v0.10.928 (palet ADIM 2, "yapı sadeleştirme",
// operatör onayı: Y1–Y4).
//
// Ne çiviliyor: dört yapı kararının CSS tarafı geri kaymasın.
//   Y1  kartlar statik — tek `.card` kuralı, `.card:hover` yok; tıklanabilir
//       kart yalnız `.card-link` ve hover'ı --border-strong (accent değil).
//   Y2  --divider, --border bildiren HER blokta da bildirilmiş (kapsanmış
//       `#sidebar` remap'i dahil); satır ayracı --divider, başlık çizgisi
//       --border.
//   Y3  tablo başlıkları düz: büyük harf yok; `thead th` --fs-xs/600/--text2.
//   Y4  `button.sec` / `a.sec` / `.ib-sec` dolgusuz, kenar --border-control.
//
// Neden ayrı kapı: bunların hiçbiri tsc/eslint/colorLeaks/undefinedCssRefs'e
// takılmaz. İkinci bir `.card {}` bloğu eklemek tamamen geçerli CSS'tir —
// Y1 öncesindeki "77 statik kartın hepsi tıklanabilir görünüyor" bozukluğu
// tam olarak böyle doğmuştu ve aynı özgüllükteki `.card-tight`i de
// sessizce yutmuştu.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const CSS = readFileSync(resolve(__dirname, 'globals.css'), 'utf8');

// Yorumları BOŞALT, silme: gerekçe yorumlarında `.card:hover`,
// `uppercase` gibi dizgiler geçiyor ve kural sanılmamalı.
const CLEAN = CSS.replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '));

interface Rule { selectors: string[]; body: string; index: number }
// `[^{}]+` süslü parantez aşamadığı için @media başlıkları kural sanılmaz;
// medya bloklarının İÇİNDEKİ kurallar ise yakalanır (surfaceTokens deseni).
const RULES: Rule[] = [...CLEAN.matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(m => ({
  selectors: m[1].split(',').map(s => s.trim().replace(/\s+/g, ' ')),
  body: m[2],
  index: m.index ?? 0,
}));
const rulesFor = (sel: string) => RULES.filter(r => r.selectors.includes(sel));
const bodyOf = (sel: string): string => {
  const r = rulesFor(sel)[0];
  if (!r) throw new Error(`kural yok: ${sel}`);
  return r.body;
};

describe('Y1 — kartlar statik', () => {
  it('tam olarak BİR `.card` kuralı var', () => {
    expect(rulesFor('.card').length, 'ikinci `.card` bloğu geri geldi — 77 kart yine tıklanabilir görünür').toBe(1);
  });

  it('`.card:hover` yok, `.card` imleç/geçiş taşımıyor', () => {
    const hovers = RULES.flatMap(r => r.selectors).filter(s => /(^|\s)\.card:hover\b/.test(s));
    expect(hovers).toEqual([]);
    const body = bodyOf('.card');
    expect(body).not.toMatch(/cursor\s*:/);
    expect(body).not.toMatch(/transition\s*:/);
  });

  it('tıklanabilir kart `.card-link`: bağlantı rengi/altı çizgisi sıfırlı, hover --border-strong', () => {
    const base = bodyOf('.card-link');
    expect(base).toMatch(/color:\s*inherit/);
    expect(base).toMatch(/text-decoration:\s*none/);
    expect(base).toMatch(/cursor:\s*pointer/);
    const hover = rulesFor('.card-link:hover')[0];
    expect(hover, '.card-link:hover yok').toBeDefined();
    expect(hover.selectors, 'klavye odağı hover ile aynı görünmeli').toContain('.card-link:focus-visible');
    expect(hover.body).toMatch(/border-color:\s*var\(--border-strong\)/);
    expect(hover.body).not.toMatch(/var\(--accent\)/);
  });

  // Seçenek A: `.card-tight` hiç uygulanmadı (ikinci `.card` onu eziyordu);
  // kural ve emisyon BİRLİKTE gitti. Biri geri gelirse 11 site sessizce
  // bg2/10px/radius-sm'e döner — undefinedCssRefs dizi içi sınıfı görmez.
  it('`.card-tight` ne CSS\'te ne Card atomunda', () => {
    expect(RULES.flatMap(r => r.selectors).filter(s => s.includes('.card-tight'))).toEqual([]);
    const card = readFileSync(resolve(__dirname, '../components/ui/Card.tsx'), 'utf8');
    expect(card).not.toMatch(/['"`]card-tight['"`]/);
  });

  it('tıklanabilir karo hover\'ları accent değil --border-strong', () => {
    const tile = bodyOf('.tile-btn:hover:not(:disabled)');
    expect(tile, 'background silinirse button:hover karoyu accent\'e boyar').toMatch(/background\s*:/);
    expect(tile).toMatch(/var\(--border-strong\)/);
    expect(bodyOf('.stat-tile-btn:hover:not(:disabled)')).toMatch(/var\(--border-strong\)/);
    const pill = bodyOf('.ud-pill:hover');
    expect(pill).toMatch(/border-color:\s*var\(--border-strong\)/);
    expect(pill).toMatch(/text-decoration:\s*none/);
    expect(pill).not.toMatch(/--accent/);
  });
});

describe('Y2 — --divider', () => {
  // D7 deseni: kapsanmış bir remap (`[data-theme="redhat"] #sidebar`)
  // --border'ı çevirip --divider'ı unutursa :root değeri koyu raya sızar.
  it('--border bildiren her blok --divider ve --border-control da bildiriyor', () => {
    const blocks = RULES.filter(r => /(^|[;\s])--border\s*:/.test(r.body));
    expect(blocks.length).toBeGreaterThanOrEqual(4);
    const bad = blocks
      .filter(r => !/(^|[;\s])--divider\s*:/.test(r.body) || !/(^|[;\s])--border-control\s*:/.test(r.body))
      .map(r => r.selectors.join(', '));
    expect(bad).toEqual([]);
  });

  it('satır ayracı --divider, başlık/gövde sınırı --border', () => {
    expect(bodyOf('tbody tr')).toMatch(/border-bottom:\s*1px solid var\(--divider\)/);
    expect(bodyOf('thead th')).toMatch(/border-bottom:\s*1px solid var\(--border\)/);
    // Yapışkan başlığın gölge-kenarı taban çizginin ikizi: aynı token.
    expect(bodyOf('.table-wrap.is-scroll thead th')).toMatch(/box-shadow:\s*inset 0 -1px 0 var\(--border\)/);
  });
});

describe('Y3 — düz tablo başlıkları', () => {
  it('`thead th` taban kuralı: --fs-xs, 600, --text2, büyük harf yok', () => {
    const body = bodyOf('thead th');
    expect(body).toMatch(/font-size:\s*var\(--fs-xs\)/);
    expect(body).toMatch(/font-weight:\s*600/);
    expect(body).toMatch(/color:\s*var\(--text2\)/);
    expect(body).toMatch(/text-transform:\s*none/);
    expect(body).toMatch(/letter-spacing:\s*normal/);
  });

  it('hiçbir tablo başlığı kuralı büyük harf dayatmıyor', () => {
    const th = /(^|[\s>+~])th([.:[\s]|$)/;
    const bad = RULES
      .filter(r => r.selectors.some(s => th.test(s)) && /text-transform:\s*uppercase/.test(r.body))
      .map(r => r.selectors.join(', '));
    expect(bad).toEqual([]);
  });
});

describe('Y4 — dolgusuz ikincil', () => {
  it('`button.sec` / `a.sec` / `.ib-sec` şeffaf, kenar --border-control', () => {
    for (const sel of ['button.sec', 'a.sec', '.btn-icon.ib-sec']) {
      const body = bodyOf(sel);
      expect(body, sel).toMatch(/background:\s*transparent/);
      expect(body, sel).toMatch(/var\(--border-control\)/);
    }
  });

  it('hover bg2 + --border-strong, basılı bg3 (her biri background yazar)', () => {
    for (const [hover, active] of [
      ['button.sec:hover:not(:disabled)', 'button.sec:active:not(:disabled)'],
      ['a.sec:hover', 'a.sec:active'],
      ['.btn-icon.ib-sec:hover:not(:disabled)', '.btn-icon.ib-sec:active:not(:disabled)'],
    ]) {
      expect(bodyOf(hover), hover).toMatch(/background:\s*var\(--bg2\)/);
      expect(bodyOf(hover), hover).toMatch(/border-color:\s*var\(--border-strong\)/);
      expect(bodyOf(active), active).toMatch(/background:\s*var\(--bg3\)/);
    }
  });

  it('`.ib-sec` `.btn-icon.active`in ÜSTÜNDE; açık ikincil anahtar hover\'da accent kalır', () => {
    const sec = rulesFor('.btn-icon.ib-sec')[0];
    const act = rulesFor('.btn-icon.active')[0];
    expect(sec.index).toBeLessThan(act.index);
    const on = bodyOf('.btn-icon.ib-sec.active:hover:not(:disabled)');
    expect(on).toMatch(/background:\s*var\(--accent-bg\)/);
    expect(on).toMatch(/border-color:\s*var\(--accent\)/);
  });

  it('içeriğin üstünde yüzen ikinciller için opak kaçış (`.is-overlay`, topoloji zoom)', () => {
    const ov = rulesFor('button.sec.is-overlay')[0];
    expect(ov, '.is-overlay kuralı yok').toBeDefined();
    expect(ov.selectors).toContain('.btn-icon.ib-sec.is-overlay');
    expect(ov.body).toMatch(/background:\s*var\(--bg1\)/);
    expect(bodyOf('.topo-zoomctl button.sec')).toMatch(/background:\s*var\(--bg1\)/);
  });
});
