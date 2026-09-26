// @vitest-environment jsdom
//
// headMenu.contract.test.tsx — v0.10.939 (tablo standardı S8, operatör
// cevabı: "Kolonları sıfırla başlık satırının sağ ucunda soluk ⋯ menüde").
// jsdom sözleşmesi, @testing-library YOK (DataTable.contract emsali):
//   • DataTableHead TEK ⋯ basar — son görünür yönetilen başlıkta, yeni hücre
//     YOK (th sayısı = kolon sayısı); `trailing` hücresi sayfanındır
//   • tık / Enter menüyü açar ve SIRALAMAZ; Shift+→ son kolonu boyutlamaz
//   • "Kolonları sıfırla" kalıcı genişlikleri temizler, menü kapanır, odak tetiğe
//   • klavye: ↓ açar, ↑/↓ gezinir, Tab kapatır; Esc tek katman kanalından
//   • dışarı tık kapatır; eylem kolonu (kind 'actions') son sıradaysa menü onda
//   • v0.10.939 (tablo standardı S8, inceleme):
//     – host th'nin adı kolon etiketi ("N"), "N Tablo seçenekleri" DEĞİL
//     – tetikte tooltip yok (ipucunun Esc katmanı menününkiyle yarışıyordu)
//     – "Kolonları sıfırla" kayıtlı genişlik yokken devre dışı
//     – kaydırma/boyut kapatması odağı menü içindeyse tetiğe verir
//     – CSS: tetik yer AYIRMAZ (kalıcı host dolgusu yok; yalnız hover:none),
//       dururken opacity 0 (visibility DEĞİL), hover/odak/açıkken belirir,
//       --bg1 zemin, boyut tutamağının solunda; açıkken ata z dropdown;
//       telefonda 36px tabanından muaf
import { describe, it, expect, afterEach, beforeEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import type { ReactNode } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { useDataTable, DataTableHead, DataTableColgroup, type DataTable, type ColumnDef } from './index';
import { topEscLayer, __resetEscLayers } from '@/lib/escLayer';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
function render(node: ReactNode): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host;
}
beforeEach(() => { __resetEscLayers(); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

interface Row { id: string; name: string; n: number }
const ROWS: Row[] = [{ id: 'a', name: 'alpha', n: 3 }, { id: 'b', name: 'beta', n: 1 }];
const COLS: ColumnDef<Row>[] = [
  { id: 'name', label: 'Name', sortValue: r => r.name, width: 120 },
  { id: 'n', label: 'N', sortValue: r => r.n, numeric: true, width: 80 },
];

let last: DataTable<Row> | null = null;
function Probe({ cols = COLS, trailing, keyName = 'head-menu' }: { cols?: ColumnDef<Row>[]; trailing?: ReactNode; keyName?: string }) {
  const dt = useDataTable<Row>({ storageKey: keyName, columns: cols, rows: ROWS });
  last = dt;
  return (
    <div className="table-wrap">
      <table {...dt.tableProps}>
        <DataTableColgroup dt={dt} />
        <DataTableHead dt={dt} trailing={trailing} />
        <tbody>{dt.sortedRows.map((r, i) => <tr key={r.id} {...dt.rowProps(i)}><td>{r.name}</td><td>{r.n}</td></tr>)}</tbody>
      </table>
    </div>
  );
}

const trigger = (el: HTMLElement) => el.querySelector<HTMLButtonElement>('.dt-menu button')!;
// Erişilebilir ad (accname'in bu testin ihtiyacı kadarı): aria-label kazanır;
// yoksa metin + çocukların adı, aria-hidden alt ağaç hariç.
function accName(el: Element): string {
  const own = el.getAttribute('aria-label');
  if (own) return own;
  let out = '';
  for (const n of Array.from(el.childNodes)) {
    if (n.nodeType === Node.TEXT_NODE) out += n.textContent ?? '';
    else if (n instanceof Element && n.getAttribute('aria-hidden') !== 'true') out += ` ${accName(n)} `;
  }
  return out.replace(/\s+/g, ' ').trim();
}
// Yorumları soy: gerekçe yorumu eski yazımı (tooltip=) anıyor.
const code = (t: string) => t.replace(/\/\*[\s\S]*?\*\//g, '').split('\n').map(l => l.replace(/\/\/.*$/, '')).join('\n');
const menu = () => document.querySelector<HTMLElement>('[role="menu"]');
const key = (t: Element, k: string, shiftKey = false) =>
  act(() => { t.dispatchEvent(new KeyboardEvent('keydown', { key: k, shiftKey, bubbles: true })); });

describe('DataTableHead ⋯ menüsü — yer', () => {
  it('tek ⋯, SON başlıkta; yeni hücre yok', () => {
    const el = render(<Probe />);
    const ths = [...el.querySelectorAll('thead th')];
    expect(ths.length).toBe(COLS.length);
    expect(el.querySelectorAll('.dt-menu').length).toBe(1);
    expect(ths[ths.length - 1].classList.contains('dt-menu-host')).toBe(true);
    expect(ths[ths.length - 1].querySelector('.dt-menu')).not.toBeNull();
    const btn = trigger(el);
    expect(btn.getAttribute('aria-label')).toBe('Tablo seçenekleri');
    expect(btn.getAttribute('aria-haspopup')).toBe('menu');
    expect(btn.getAttribute('aria-expanded')).toBe('false');
    expect(btn.tabIndex).toBe(0); // klavyeyle ulaşılır
    expect(el.querySelectorAll('col').length).toBe(COLS.length); // colgroup değişmedi
  });

  it('trailing hücresi sayfanın — menü son YÖNETİLEN başlıkta', () => {
    const el = render(<Probe trailing={<th className="page-owned" />} />);
    expect(el.querySelector('th.page-owned .dt-menu')).toBeNull();
    const managed = [...el.querySelectorAll('thead th')].filter(t => !t.classList.contains('page-owned'));
    expect(managed[managed.length - 1].querySelector('.dt-menu')).not.toBeNull();
  });

  it('eylem kolonu sonda → menü boş başlıklı eylem hücresinde', () => {
    const cols: ColumnDef<Row>[] = [...COLS, { id: 'act', label: '', kind: 'actions', width: 60 }];
    const el = render(<Probe cols={cols} keyName="head-menu-act" />);
    const actTh = el.querySelector('thead th.col-actions')!;
    expect(actTh.querySelector('.dt-menu')).not.toBeNull();
    expect(el.querySelectorAll('.dt-menu').length).toBe(1);
  });

  it('yalnız eylem kolonu varsa (boyutlanacak kolon yok) menü yok', () => {
    const cols: ColumnDef<Row>[] = [{ id: 'act', label: '', kind: 'actions', width: 60 }];
    const el = render(<Probe cols={cols} keyName="head-menu-only-act" />);
    expect(el.querySelector('.dt-menu')).toBeNull();
  });

  // v0.10.939 (tablo standardı S8) — ⋯ başlığın İÇİNDE; host th'ye ad
  // verilmezse columnheader adı içerikten hesaplanır ve tetiğin aria-label'ı
  // eklenir: ekran okuyucu son kolonu "N Tablo seçenekleri" diye okurdu.
  it('host th\'nin erişilebilir adı kolon etiketi — "Tablo seçenekleri" içermez', () => {
    const el = render(<Probe keyName="head-menu-name" />);
    const ths = [...el.querySelectorAll<HTMLElement>('thead th')];
    const host = ths[ths.length - 1];
    expect(host.classList.contains('dt-menu-host')).toBe(true);
    expect(accName(host)).toBe('N');
    expect(accName(host)).not.toContain('Tablo seçenekleri');
    // Tetik kendi adıyla ayrı bir düğme olarak kalır.
    expect(accName(trigger(el))).toBe('Tablo seçenekleri');
    // Host olmayan başlık dokunulmadı (ad içerikten).
    expect(ths[0].getAttribute('aria-label')).toBeNull();
  });

  it('eylem kolonu host ise ad yine etiketi ("Actions")', () => {
    const cols: ColumnDef<Row>[] = [...COLS, { id: 'act', label: 'Actions', kind: 'actions', width: 60 }];
    const el = render(<Probe cols={cols} keyName="head-menu-act-name" />);
    const actTh = el.querySelector<HTMLElement>('thead th.col-actions')!;
    expect(actTh.querySelector('.dt-menu')).not.toBeNull();
    expect(accName(actTh)).toBe('Actions');
  });
});

describe('DataTableHead ⋯ menüsü — davranış', () => {
  it('tık menüyü açar, SIRALAMAZ; "Kolonları sıfırla" genişlikleri temizler', () => {
    const el = render(<Probe />);
    act(() => { last!.resizeBy('name', 40); });
    expect(Object.keys(last!.colWidths)).toEqual(['name']);
    const sortBefore = last!.sort;
    act(() => { trigger(el).click(); });
    expect(last!.sort).toEqual(sortBefore);
    expect(trigger(el).getAttribute('aria-expanded')).toBe('true');
    const m = menu()!;
    expect(m.getAttribute('aria-label')).toBe('Tablo seçenekleri');
    expect(trigger(el).getAttribute('aria-controls')).toBe(m.id);
    const items = [...m.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')];
    expect(items.map(i => i.textContent)).toEqual(['Kolonları sıfırla']);
    expect(document.activeElement).toBe(items[0]); // açılışta ilk öğe odakta
    act(() => { items[0].click(); });
    expect(last!.colWidths).toEqual({});
    expect(last!.sort).toEqual(sortBefore);
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(trigger(el));
  });

  it('Enter / Shift+→ tetikte başlığa kabarcıklanmaz (sıralama/boyut yok)', () => {
    const el = render(<Probe keyName="head-menu-keys" />);
    const sortBefore = last!.sort;
    key(trigger(el), 'Enter');
    key(trigger(el), 'ArrowRight', true);
    expect(last!.sort).toEqual(sortBefore);
    expect(last!.colWidths).toEqual({});
  });

  it('↓ açar; ↑/↓/Home/End gezinir; Tab kapatır ve odağı tetiğe verir', () => {
    const el = render(<Probe keyName="head-menu-nav" />);
    act(() => { last!.resizeBy('name', 16); }); // öğe etkin olsun (kayıtlı genişlik)
    key(trigger(el), 'ArrowDown');
    const m = menu()!;
    expect(m).not.toBeNull();
    const first = m.querySelector('[role="menuitem"]')!;
    expect(document.activeElement).toBe(first);
    key(first, 'ArrowDown');
    expect(document.activeElement).toBe(first); // tek öğe: döngü kendine
    key(first, 'End');
    expect(document.activeElement).toBe(first);
    key(first, 'Tab');
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(trigger(el));
  });

  it('Esc yalnız açıkken katman ekler ve menüyü kapatır', () => {
    const el = render(<Probe keyName="head-menu-esc" />);
    expect(topEscLayer()).toBeNull();
    act(() => { trigger(el).click(); });
    const esc = topEscLayer();
    expect(esc).not.toBeNull();
    act(() => { esc!(); });
    expect(menu()).toBeNull();
    expect(topEscLayer()).toBeNull();
    expect(document.activeElement).toBe(trigger(el));
  });

  it('dışarı basış kapatır; menü içi basış kapatmaz', () => {
    const el = render(<Probe keyName="head-menu-outside" />);
    act(() => { trigger(el).click(); });
    act(() => { menu()!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })); });
    expect(menu()).not.toBeNull();
    act(() => { document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })); });
    expect(menu()).toBeNull();
  });

  // v0.10.939 (tablo standardı S8) — sıfırlanacak bir şey yokken öğe devre
  // dışı; menü yine açılır, odak menü kabında (Tab/Esc çalışır).
  it('"Kolonları sıfırla" kayıtlı genişlik yokken devre dışı; olunca etkin', () => {
    const el = render(<Probe keyName="head-menu-disabled" />);
    expect(Object.keys(last!.colWidths)).toEqual([]);
    act(() => { trigger(el).click(); });
    const item = menu()!.querySelector<HTMLButtonElement>('[role="menuitem"]')!;
    expect(item.textContent).toBe('Kolonları sıfırla');
    // aria-disabled: öğe odak alır ve okunur, ama tık bir şey yapmaz.
    expect(item.getAttribute('aria-disabled')).toBe('true');
    expect(item.disabled).toBe(false);
    expect(document.activeElement).toBe(item);
    act(() => { item.click(); });
    expect(menu()).not.toBeNull();
    key(menu()!, 'Tab');
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(trigger(el));
    act(() => { last!.resizeBy('n', 8); });
    act(() => { trigger(el).click(); });
    const enabled = menu()!.querySelector<HTMLButtonElement>('[role="menuitem"]')!;
    expect(enabled.hasAttribute('aria-disabled')).toBe(false);
    expect(document.activeElement).toBe(enabled);
  });

  // v0.10.939 (tablo standardı S8) — kaydırma kapatması menüyü söker; odak
  // menünün içindeyse tetiğe döner (yoksa body'ye düşerdi). Odak başka
  // yerdeyse dokunulmaz.
  it('kaydırma / boyut menüyü kapatır; odak menüdeyse tetiğe döner', () => {
    const el = render(<Probe keyName="head-menu-scroll" />);
    act(() => { last!.resizeBy('name', 16); });
    act(() => { trigger(el).click(); });
    expect(menu()!.contains(document.activeElement)).toBe(true);
    act(() => { document.dispatchEvent(new Event('scroll')); });
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(trigger(el));

    const other = document.createElement('input');
    document.body.appendChild(other);
    try {
      act(() => { trigger(el).click(); });
      act(() => { other.focus(); });
      act(() => { window.dispatchEvent(new Event('resize')); });
      expect(menu()).toBeNull();
      expect(document.activeElement).toBe(other);
    } finally { other.remove(); }
  });

  // v0.10.939 (tablo standardı S8) — ipucu yok: aria-label ad için yeter;
  // ipucu kendi Esc katmanını açıp menünün Esc'ini gölgeliyordu.
  it('tetikte tooltip yok (odakta ipucu / Esc katmanı açılmaz)', () => {
    const el = render(<Probe keyName="head-menu-notip" />);
    act(() => { trigger(el).focus(); });
    expect(document.querySelector('[role="tooltip"]')).toBeNull();
    expect(topEscLayer()).toBeNull();
    expect(trigger(el).parentElement!.classList.contains('dt-menu')).toBe(true);
    expect(code(readFileSync(resolve(__dirname, 'HeadMenu.tsx'), 'utf8'))).not.toMatch(/\btooltip(Side)?=/);
  });

  it('menü position:fixed sınıfla (dt-menu-pop) çizilir, koordinat satır içi', () => {
    const el = render(<Probe keyName="head-menu-pos" />);
    act(() => { trigger(el).click(); });
    const m = menu()!;
    expect(m.classList.contains('dt-menu-pop')).toBe(true);
    expect(m.style.left).toMatch(/px$/);
    expect(m.style.top).toMatch(/px$/);
  });
});

// ── CSS sözleşmesi (globals.css) ──────────────────────────────────────────
// v0.10.939 (tablo standardı S8, inceleme) — ⋯ son kolonun etiketini
// KAYDIRMAZ. Önceki `thead th.dt-menu-host { padding-right: 30px }` (telefonda
// 46px) her tabloda son başlığın etiketini/okunu sola itiyordu. Tetik th'nin
// sağ ucuna mutlak konumla biner; dururken görünmez (opacity 0 — `.sort-arrow`
// fikri), başlık satırı hover / host odak / açıkken belirir. Yalnız hover'ı
// olmayan (dokunmatik) cihazda host dolgusu ayrılır.
interface CssRule { media: string; selectors: string[]; body: string }
function parseCss(src: string): CssRule[] {
  const clean = src.replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '));
  const out: CssRule[] = [];
  const stack: string[] = [];
  let buf = '';
  for (const ch of clean) {
    if (ch === '{') { stack.push(buf.slice(buf.lastIndexOf(';') + 1).trim()); buf = ''; }
    else if (ch === '}') {
      const pre = stack.pop() ?? '';
      if (!pre.startsWith('@')) {
        out.push({
          media: stack.filter(p => p.startsWith('@media')).join(' '),
          selectors: pre.split(',').map(x => x.trim().replace(/\s+/g, ' ')),
          body: buf,
        });
      }
      buf = '';
    } else buf += ch;
  }
  return out;
}
const RULES = parseCss(readFileSync(resolve(__dirname, '../../../styles/globals.css'), 'utf8'));
const rulesWith = (sel: string, media = '') => RULES.filter(r => r.selectors.includes(sel) && r.media === media);
const bodyOf = (sel: string, media = ''): string => {
  const r = rulesWith(sel, media);
  if (r.length === 0) throw new Error(`kural yok: ${sel} ${media}`);
  return r.map(x => x.body).join(';');
};

describe('⋯ CSS — tetik yer ayırmaz, üstüne biner', () => {
  it('host th\'ye kalıcı dolgu YOK; dolgu yalnız @media (hover: none) içinde', () => {
    const padded = RULES.filter(r => r.selectors.some(x => x.includes('dt-menu-host')) && /padding/.test(r.body));
    expect(padded.length).toBeGreaterThan(0);
    for (const r of padded) {
      expect(r.media, `${r.selectors.join(', ')} dolgusu hover:none dışında`).toBe('@media (hover: none)');
    }
    expect(bodyOf('thead th.dt-menu-host', '@media (hover: none)')).toMatch(/padding-right:\s*30px/);
    expect(bodyOf('[data-density] thead th.dt-menu-host', '@media (hover: none)')).toMatch(/padding-right:\s*30px/);
    // Telefonun 46px ikizi gitti.
    expect(RULES.filter(r => r.media.includes('max-width: 640px') && r.selectors.some(x => x.includes('dt-menu-host')))).toEqual([]);
  });

  it('tetik th\'nin sağ ucunda mutlak; boyut tutamağını (6px) örtmez', () => {
    const menu = bodyOf('.dt-menu');
    expect(menu).toMatch(/position:\s*absolute/);
    expect(menu).toMatch(/top:\s*0/);
    expect(menu).toMatch(/bottom:\s*0/);
    expect(menu).toMatch(/right:\s*6px/);
    const handle = bodyOf('thead th .col-resize-handle');
    expect(handle).toMatch(/right:\s*0/);
    expect(handle).toMatch(/width:\s*6px/);
  });

  it('dururken görünmez (opacity 0, visibility DEĞİL — Tab sırasında kalır), zemini --bg1', () => {
    const btn = bodyOf('.dt-menu .btn-icon');
    expect(btn).toMatch(/opacity:\s*0\b/);
    expect(btn).toMatch(/background:\s*var\(--bg1\)/);
    expect(btn).not.toMatch(/visibility\s*:/);
    expect(RULES.filter(r => r.selectors.some(x => x.includes('dt-menu')) && /visibility:\s*hidden|display:\s*none/.test(r.body))).toEqual([]);
  });

  it('başlık satırı hover / host odak / açık tetik belirtir', () => {
    for (const sel of [
      'th.dt-menu-host:hover .dt-menu .btn-icon',
      '.dt-menu-host--dirty .dt-menu .btn-icon',
      '.dt-menu-host:focus-within .dt-menu .btn-icon',
      '.dt-menu .btn-icon[aria-expanded="true"]',
    ]) {
      expect(bodyOf(sel), sel).toMatch(/opacity:\s*1\b/);
    }
    // Dokunmatikte hover yok → hep görünür.
    expect(bodyOf('.dt-menu .btn-icon', '@media (hover: none)')).toMatch(/opacity:\s*1\b/);
  });

  it('son kolonun etkin oku dururken görünür (tetik opak değil, ok kuralı yerinde)', () => {
    expect(bodyOf('thead th.sorted .sort-arrow')).toMatch(/opacity:\s*1\b/);
    // Ok ya da etiket gizleyen bir dt-menu-host kuralı yok.
    expect(RULES.filter(r => r.selectors.some(x => /dt-menu-host.*sort-arrow/.test(x)))).toEqual([]);
  });

  it('odak halkası içeri (-2px): th overflow:hidden kırpmasın', () => {
    expect(bodyOf('.dt-menu .btn-icon:focus-visible')).toMatch(/outline-offset:\s*-2px/);
  });

  it('telefonda 36px tabanından muaf (.th-remove gibi); dokunma hedefi th yüksekliği', () => {
    const phone = '@media (max-width: 640px)';
    const exempt = RULES.find(r => r.media === phone && r.selectors.includes('.btn-icon.th-remove') && r.selectors.includes('.dt-menu .btn-icon'));
    expect(exempt, '.dt-menu .btn-icon th-remove muafiyet listesinde değil').toBeDefined();
    expect(exempt!.body).toMatch(/min-height:\s*0/);
    expect(bodyOf('.dt-menu .btn-icon', phone)).toMatch(/height:\s*100%/);
  });

  // `:has(.tip)` emsalinin ikizi: yapışkan ata (vt-scroll thead z 2,
  // is-scroll th z 3, sabit kolon başlığı) kendi yığın bağlamını kurar; menü
  // açıkken ata dropdown basamağına çıkmazsa sonraki satırın yapışkan hücresi
  // menüyü örter. Seçiciler taban kuralını ÖZGÜLLÜKTE yenmeli — çıplak
  // `thead:has(…)` / `thead th:has(…)` yeniliyordu.
  it('menü açıkken yapışkan ata z dropdown (özgüllükte yenen seçicilerle)', () => {
    for (const sel of [
      '.vt-scroll thead:has(.dt-menu-pop)',
      '.table-wrap.is-scroll thead th:has(.dt-menu-pop)',
      'th.sticky-right:has(.dt-menu-pop)',
      'th.sticky-left:has(.dt-menu-pop)',
    ]) {
      expect(bodyOf(sel), sel).toMatch(/z-index:\s*var\(--z-dropdown\)/);
    }
    expect(rulesWith('thead:has(.dt-menu-pop)')).toEqual([]);
    expect(rulesWith('thead th:has(.dt-menu-pop)')).toEqual([]);
  });
});
