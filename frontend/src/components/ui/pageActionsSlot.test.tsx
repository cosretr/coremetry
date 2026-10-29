// @vitest-environment jsdom
//
// pageActionsSlot — v0.10.1319 K1 kapısı: sayfa birincil eylem yuvası.
//
// Operatör onaylı yerleşim: filtreler + sonuç bilgisi SOLDA, eylemler
// SAĞDA; ikinciller tek primary'nin solunda; primary md boy; telefonda
// eylemler KATLANMAZ (panelin dışında, sayaca girmez).
//
// Üç katman çiviliyor:
//   A) PageControls mount — yuva DOM'da son, className yalnız yuva
//      varken değişiyor, dar ekranda panelin DIŞINDA.
//   B) Kaynak — 6 dönüştürülen sayfa primary'yi `actions=` yuvasında
//      taşıyor; repo genelinde çocuklarında primary kalan bar sayısı
//      yukarı çıkamıyor (ratchet).
//   C) CSS — yuva kuralları ve telefon katmanı.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { PageControls } from './PageControls';
import { __resetEscLayers } from '@/lib/escLayer';
import { jsxOpenTags } from '@/styles/jsxTags';

// ── matchMedia sahtesi (pageControlsCollapse.test.tsx'ten kopya) ──────
function setViewport(narrow: boolean) {
  window.matchMedia = ((q: string) => ({
    matches: narrow,
    media: q,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}

let root: Root | null = null;
let host: HTMLDivElement | null = null;

function render(ui: React.ReactElement) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(ui); });
}

const bar = () => host!.querySelector('.controls') as HTMLDivElement;
const moreBtn = () => host!.querySelector('.pc-more') as HTMLButtonElement | null;

beforeEach(() => { __resetEscLayers(); });
afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

describe('v0.10.1319 K1 — masaüstü', () => {
  beforeEach(() => setViewport(false));

  it('.pc-actions barın SON çocuğu, className "controls has-actions"', () => {
    render(
      <PageControls actions={<button>+ New</button>}>
        <input placeholder="q" />
        <span>3 items</span>
      </PageControls>,
    );
    const b = bar();
    expect(b.className).toBe('controls has-actions');
    const last = b.lastElementChild as HTMLElement;
    expect(last.className).toBe('pc-actions');
    expect(last.textContent).toBe('+ New');
    expect(b.children.length).toBe(3);
  });

  it('actions YOKSA yuva basılmıyor ve className/DOM eski hâlle birebir', () => {
    const kids = (<><input placeholder="q" /><span>3 items</span></>);
    render(<PageControls sticky>{kids}</PageControls>);
    const without = bar().outerHTML;
    expect(bar().className).toBe('controls is-sticky');
    expect(host!.querySelector('.pc-actions')).toBeNull();
    act(() => { root!.unmount(); });
    host!.remove();

    // `actions={isAdmin && …}` yetkisizde false geçer — o da "yok" demek.
    render(<PageControls sticky actions={false}>{kids}</PageControls>);
    expect(bar().outerHTML).toBe(without);
    act(() => { root!.unmount(); });
    host!.remove();

    render(<PageControls sticky actions={null}>{kids}</PageControls>);
    expect(bar().outerHTML).toBe(without);
  });

  it('sticky + className ile sınıf sırası korunuyor', () => {
    render(<PageControls sticky className="x" actions={<button>a</button>}><input /></PageControls>);
    expect(bar().className).toBe('controls is-sticky has-actions x');
  });
});

describe('v0.10.1319 K1 — dar ekran (≤640px)', () => {
  beforeEach(() => setViewport(true));

  it('.pc-actions doğrudan çocuk, disclosure düğmesinden SONRA, panelin İÇİNDE DEĞİL', () => {
    render(
      <PageControls actions={<button>+ New</button>}>
        <input placeholder="q" />
        <select><option>a</option></select>
        <span>meta</span>
      </PageControls>,
    );
    const b = bar();
    expect(b.className).toBe('controls is-collapsible has-actions');
    const acts = b.querySelector('.pc-actions') as HTMLElement;
    expect(acts).not.toBeNull();
    expect(acts.parentElement).toBe(b);
    expect(acts.closest('.pc-panel')).toBeNull();
    const kids = Array.from(b.children);
    const iMore = kids.indexOf(moreBtn()!);
    const iActs = kids.indexOf(acts);
    const iPanel = kids.findIndex(k => k.classList.contains('pc-panel'));
    expect(iMore).toBeGreaterThanOrEqual(0);
    expect(iActs).toBe(iMore + 1);
    expect(iPanel).toBe(iActs + 1);
  });

  it('actions içindeki bir input filtre alanı SAYILMIYOR', () => {
    render(
      <PageControls actions={<><input type="file" style={{ display: 'none' }} /><button>Import</button></>}>
        <input placeholder="q" />
        <select><option>a</option></select>
      </PageControls>,
    );
    // Yalnız panel içindeki select sayılıyor.
    expect(moreBtn()!.textContent).toContain('Filtreler (1)');
  });

  it('actions yoksa dar ekranda da yuva/sınıf yok', () => {
    render(<PageControls><input /><select /></PageControls>);
    expect(bar().className).toBe('controls is-collapsible');
    expect(host!.querySelector('.pc-actions')).toBeNull();
  });
});

// ── B) Kaynak çivileri ────────────────────────────────────────────────
const SRC = resolve(__dirname, '../..');
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8');

// Blok yorumları + satır başı `//` yorumlarını sıyır. Tam bir TS soyucu
// (zLayers'taki) JSX metnindeki kesme işaretini (`aren't`) dizgi açılışı
// sanıyor; burada ölçülen tek şey `variant="primary"` geçişi.
const stripComments = (s: string) =>
  s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');

// `{` konumundan dengeli kapanışa kadar (dizgi-farkında).
function braceValue(src: string, open: number): string {
  let depth = 0; let q: string | null = null;
  for (let i = open; i < src.length; i++) {
    const c = src[i];
    if (q) { if (c === '\\') { i++; continue; } if (c === q) q = null; continue; }
    if (c === '"' || c === "'" || c === '`') { q = c; continue; }
    if (c === '{') depth++;
    else if (c === '}') { depth--; if (depth === 0) return src.slice(open, i + 1); }
  }
  return src.slice(open);
}

interface Bar { tag: string; actions: string | null; children: string; line: number }
function pageControlsBars(rawSrc: string): Bar[] {
  const src = stripComments(rawSrc);
  return jsxOpenTags(src, 'PageControls').map(t => {
    const start = src.indexOf(t.tag);
    const end = start + t.tag.length;
    const close = t.tag.endsWith('/>') ? end : src.indexOf('</PageControls>', end);
    const a = t.tag.search(/\sactions=\{/);
    const actions = a < 0 ? null : braceValue(t.tag, t.tag.indexOf('{', a));
    return { tag: t.tag, actions, children: src.slice(end, close < 0 ? end : close), line: t.line };
  });
}
const primaries = (s: string) => (s.match(/variant="primary"/g) ?? []).length;

const CONVERTED: Record<string, string> = {
  'pages/Users.tsx': '+ New user',
  'pages/Monitors.tsx': '+ New monitor',
  'pages/Incidents.tsx': '+ Declare incident',
  'pages/Dashboards.tsx': '+ New dashboard',
  'pages/Slos.tsx': '+ New SLO',
  'pages/Alerts.tsx': '+ New alert rule',
};

describe('v0.10.1319 K1 — dönüştürülen 6 sayfa primary\'yi actions yuvasında taşıyor', () => {
  for (const [rel, label] of Object.entries(CONVERTED)) {
    it(`${rel}`, () => {
      const bars = pageControlsBars(read(rel));
      expect(bars.length, 'PageControls bulunamadı').toBe(1);
      const [b] = bars;
      expect(b.actions, '`actions=` yuvası yok').not.toBeNull();
      expect(primaries(b.actions!), 'actions içinde TEK primary').toBe(1);
      expect(b.actions!).toContain(label);
      expect(primaries(b.children), 'çocuklarda primary kalmamalı').toBe(0);
      // Sağa itme `.pc-actions`ta; çocuklarda satır-içi auto margin kalmasın.
      expect(b.children).not.toMatch(/marginLeft:\s*'auto'/);
      expect(b.actions!).not.toMatch(/marginLeft:\s*'auto'/);
    });
  }
});

describe('v0.10.1319 K1 — repo geneli ratchet', () => {
  const walk = (d: string, out: string[] = []): string[] => {
    for (const e of readdirSync(d)) {
      const p = join(d, e);
      if (statSync(p).isDirectory()) walk(p, out);
      else if (p.endsWith('.tsx') && !/\.test\.tsx$/.test(p)) out.push(p);
    }
    return out;
  };
  // Kalan meşru durumlar: filtre barının KENDİ "Run/Search" primary'si
  // (Traces / Services / Logs) — sayfa eylemi değil, filtre tetikleyicisi.
  // Sayı yalnız AŞAĞI iner.
  const MAX_PRIMARY_IN_CHILDREN = 3;

  it('çocuklarında primary taşıyan PageControls dosyası tavanı aşmıyor', () => {
    const offenders = walk(SRC)
      .map(p => ({ rel: p.slice(SRC.length + 1), src: readFileSync(p, 'utf8') }))
      .filter(f => f.src.includes('<PageControls'))
      .filter(f => pageControlsBars(f.src).some(b => primaries(b.children) > 0))
      .map(f => f.rel);
    expect(offenders.length, offenders.join(', ')).toBeLessThanOrEqual(MAX_PRIMARY_IN_CHILDREN);
    for (const rel of Object.keys(CONVERTED)) expect(offenders).not.toContain(rel);
  });

  it('tarama boş dönmüyor', () => {
    const n = walk(SRC).filter(p => readFileSync(p, 'utf8').includes('<PageControls')).length;
    expect(n).toBeGreaterThan(10);
  });
});

describe('v0.10.1319 K1 — sayfa özel çiviler', () => {
  it('Alerts: etiket takası yok, form footer ActionRow', () => {
    const src = read('pages/Alerts.tsx');
    expect(src).not.toContain("showForm ? 'Cancel'");
    expect(src).toMatch(/<ActionRow[\s/>]/);
    expect(src).toContain('onClick={startNew}');
    // Empty-state CTA yerinde (lib/errorLeg.test.ts ona bakıyor).
    expect(src).toContain('+ New rule</Button>');
  });

  it('Slos: + New SLO md boy (size="sm" yok)', () => {
    const src = read('pages/Slos.tsx');
    const bar = pageControlsBars(src)[0];
    expect(bar.actions).toContain('+ New SLO');
    expect(bar.actions).not.toMatch(/size="sm"/);
  });

  it('Dashboards: gizli dosya girdisi barın DIŞINDA', () => {
    const bar = pageControlsBars(read('pages/Dashboards.tsx'))[0];
    expect(bar.children).not.toContain('type="file"');
    expect(bar.actions).not.toContain('type="file"');
    expect(read('pages/Dashboards.tsx')).toContain('type="file"');
  });
});

// ── C) CSS ────────────────────────────────────────────────────────────
describe('v0.10.1319 K1 — CSS yuva kuralları', () => {
  // v0.10.1611 — öğe kuralları `:where(:not([data-slot]))` korumalı; taban seçiciyle oku.
  const css = read('styles/globals.css').replace(/\/\*[\s\S]*?\*\//g, '').split(':where(:not([data-slot]))').join('');
  const rule = (sel: string) => {
    const i = css.indexOf(sel + ' {');
    return i < 0 ? '' : css.slice(i, css.indexOf('}', i));
  };

  it('.controls > .pc-actions sağ kümeyi kuruyor', () => {
    const r = rule('.controls > .pc-actions');
    expect(r).not.toBe('');
    expect(r).toMatch(/display:\s*inline-flex/);
    expect(r).toMatch(/margin-left:\s*auto/);
    expect(r).toMatch(/flex-shrink:\s*0/);
    expect(r).toMatch(/gap:\s*var\(--sp-4\)/);
    expect(css).toMatch(/--sp-4:/);
  });

  it('telefon katmanı: has-actions sarıyor, yuva kendi satırında sağa yaslı', () => {
    const phone = css.slice(css.indexOf('@media (max-width: 640px)'));
    const wrap = phone.indexOf('.controls.is-collapsible.has-actions {');
    const slot = phone.indexOf('.controls.is-collapsible > .pc-actions {');
    expect(wrap).toBeGreaterThan(0);
    expect(slot).toBeGreaterThan(0);
    // Katman sırası: `nowrap` kuralından SONRA gelmeli (aynı blok).
    expect(wrap).toBeGreaterThan(phone.indexOf('.controls.is-collapsible { flex-wrap: nowrap; }'));
    const w = phone.slice(wrap, phone.indexOf('}', wrap));
    expect(w).toMatch(/flex-wrap:\s*wrap/);
    const s = phone.slice(slot, phone.indexOf('}', slot));
    expect(s).toMatch(/flex:\s*1 0 100%/);
    expect(s).toMatch(/justify-content:\s*flex-end/);
    expect(s).toMatch(/margin-left:\s*0/);
  });
});

// ── v0.10.1322 — yoğunluk katmanı yuvayı KÜÇÜLTMÜYOR ───────────────────
// compact/dense'in `.controls button` dolgu/yazı kuralı v0.10.1319'dan beri
// `.pc-actions` içindeki "+ New …" primary'sini de 3px 8px / 11px'e
// (dense 2px 6px / 10px) indiriyordu. Yuva artık seçici kümesinin DIŞINDA:
// yuvadaki md buton taban `button` metriklerini (5px 14px / 13px) giyer,
// filtre barı butonları birebir eskisi gibi küçülür.
describe('v0.10.1322 — yoğunluk: eylem yuvası md kalıyor', () => {
  // v0.10.1611 — öğe kuralları `:where(:not([data-slot]))` korumalı; taban seçiciyle oku.
  const css = read('styles/globals.css').replace(/\/\*[\s\S]*?\*\//g, '').split(':where(:not([data-slot]))').join('');
  const PHONE = css.indexOf('@media (max-width: 640px)');
  // Taban katmandaki (telefon katmanından önce) kurallar: [seçiciler, gövde].
  const baseRules = (): Array<{ sels: string[]; body: string }> => {
    const out: Array<{ sels: string[]; body: string }> = [];
    const re = /([^{}]+)\{([^{}]*)\}/g;
    const head = css.slice(0, PHONE);
    let m: RegExpExecArray | null;
    while ((m = re.exec(head))) {
      out.push({ sels: m[1].split(',').map(s => s.trim().replace(/\s+/g, ' ')), body: m[2] });
    }
    return out;
  };
  const RULES = baseRules();
  const prop = (body: string, p: string) =>
    new RegExp(`(?:^|[;\\s])${p}\\s*:\\s*([^;]+)`).exec(body)?.[1].trim();

  const LEVELS = [
    { d: 'compact', pad: '3px 8px', fs: '11px' },
    { d: 'dense', pad: '2px 6px', fs: '10px' },
  ] as const;

  // Sabit DOM: bar'ın doğrudan çocuğu buton, panel/sarmalayıcı içindeki
  // buton, yuvadaki md primary + ikincil, yuvada iç içe buton.
  const mount = (d: string) => {
    const root = document.createElement('div');
    root.setAttribute('data-density', d);
    root.innerHTML = `
      <div class="controls has-actions">
        <button id="direct">Run</button>
        <div class="pc-panel"><span><button id="nested">Clear</button></span></div>
        <button class="btn-disclose dsc-row pc-more" id="more">Filtreler</button>
        <div class="pc-actions">
          <button class="sec" id="sec">Import</button>
          <button id="primary">+ New</button>
          <span><button id="deep">x</button></span>
        </div>
      </div>
      <div class="pc-actions"><button id="outside">dış</button></div>`;
    document.body.appendChild(root);
    return root;
  };

  for (const { d, pad, fs } of LEVELS) {
    const sizing = () => RULES.filter(r =>
      r.sels.some(s => s.startsWith(`[data-density="${d}"] .controls`) && /button/.test(s))
      && prop(r.body, 'padding') !== undefined);

    it(`${d}: filtre barı dolgu/yazı kuralı birebir (${pad} / ${fs}), input/select aynı kuralda`, () => {
      const rs = sizing();
      expect(rs.length, `${d} buton boyut kuralı tek olmalı`).toBe(1);
      const r = rs[0];
      expect(prop(r.body, 'padding')).toBe(pad);
      expect(prop(r.body, 'font-size')).toBe(fs);
      expect(r.sels).toEqual([
        `[data-density="${d}"] .controls input`,
        `[data-density="${d}"] .controls select`,
        `[data-density="${d}"] .controls > button`,
        `[data-density="${d}"] .controls > :where(:not(.pc-actions)) button`,
      ]);
      // Çıplak `.controls button` artık boyut kuralında DEĞİL (yuvayı yakalardı).
      expect(r.sels).not.toContain(`[data-density="${d}"] .controls button`);
    });

    it(`${d}: seçici kümesi yuvayı ATLIYOR, filtre barı butonlarını yakalıyor`, () => {
      const root = mount(d);
      const sels = sizing()[0].sels.filter(s => /button$/.test(s));
      const hit = (id: string) => sels.some(s => root.querySelector(`#${id}`)!.matches(s));
      for (const id of ['direct', 'nested', 'more']) expect(hit(id), `#${id} küçülmeli`).toBe(true);
      for (const id of ['sec', 'primary', 'deep', 'outside']) expect(hit(id), `#${id} md kalmalı`).toBe(false);
      // Eski kümeyle fark YALNIZ yuva: bar içindeki her buton için
      // yeni küme == eski `.controls button` ∧ ¬(.pc-actions içinde).
      for (const b of Array.from(root.querySelectorAll('.controls button'))) {
        const inSlot = b.closest('.pc-actions') !== null;
        expect(sels.some(s => b.matches(s)), `#${b.id}`).toBe(!inSlot);
      }
      root.remove();
    });

    it(`${d}: yuvadaki md buton taban metrikleri (5px 14px / 13px) + isabet tabanı`, () => {
      const base = RULES.find(r => r.sels.length === 1 && r.sels[0] === 'button' && prop(r.body, 'padding'));
      expect(base, 'taban `button` kuralı').toBeDefined();
      expect(prop(base!.body, 'padding')).toBe('5px 14px');
      expect(prop(base!.body, 'font-size')).toBe('13px');
      // Yuva butonunu küçülten başka bir yoğunluk kuralı yok.
      const root = mount(d);
      const primary = root.querySelector('#primary')!;
      const shrinkers = RULES.filter(r =>
        r.sels.some(s => s.startsWith('[data-density') && primary.matches(s))
        && (prop(r.body, 'padding') !== undefined || prop(r.body, 'font-size') !== undefined));
      expect(shrinkers.map(r => r.sels.join(', '))).toEqual([]);
      // `min-height: var(--hit-min)` çıplak `.controls button` kuralında kalıyor (yuvayı da kapsar).
      const mh = RULES.find(r => r.sels.includes(`[data-density="${d}"] .controls button`)
        && prop(r.body, 'min-height') === 'var(--hit-min)');
      expect(mh, `${d} isabet tabanı kuralı`).toBeDefined();
      expect(primary.matches(`[data-density="${d}"] .controls button`)).toBe(true);
      root.remove();
    });
  }

  it('telefon katmanı değişmedi: `[data-density] .controls button` 9px 14px / 14px', () => {
    const phone = css.slice(PHONE);
    expect(phone).toMatch(/button, \[data-density\] \.controls button \{ padding: 9px 14px; font-size: 14px; \}/);
    expect(phone).not.toMatch(/pc-actions button/);
  });
});
