// platformCss.pin — v0.10.1339 (Vercel Web Interface Guidelines, denetim R1:
// ucuz platform kazanımları).
//
// Ne çiviliyor (globals.css, yorumlar soyulmuş):
//   1  `:where(button, a, .btn-icon, [role="button"], summary, label[for])`
//      `touch-action: manipulation` + `-webkit-tap-highlight-color:
//      transparent` taşıyor. `:where()` ŞART: özgüllük 0 olmazsa kaynakta
//      DAHA SONRA gelen bu kural, eşit özgüllükteki bir sürükleme
//      tutamağının `touch-action: none`ını ezer ve dokunmatik sürükleme
//      sessizce ölür (D6.5, noMouseOnlyDrag).
//   2  Sürükleme tutamaklarının `touch-action: none`ı yerinde.
//   3  Kaydırılabilir katmanlar `overscroll-behavior: contain` (modal,
//      çekmece, popover, başlık menüsü, tamamlama/seçici listesi, telefon paneli) —
//      ve her seçici bileşen kaynağında gerçekten KULLANILIYOR (ölü sınıfı
//      çivilemek kapıyı yeşil ama işlevsiz bırakırdı).
//   4  `scroll-margin-top` çapa hedeflerinde `--controls-h` (yapışkan bar)
//      üzerinden; her çapa id'si kaynakta gerçekten bir hedef.
//   5  iOS 16px yakınlaştırma kuralı telefon katmanında (D2.2) — tekrar
//      yazılmadı, burada ikinci kez çivileniyor.
//   6  v0.10.1363 — kök (`html, body`) `overscroll-behavior-y: none`; yatay
//      eksen SERBEST (tarayıcı geri jesti), kısaltma `overscroll-behavior` yok.
//
// Neden kapı: bu dört kuralın hiçbiri masaüstünde görünmez; silinseler
// tsc/eslint/diğer stil kapıları susar, kusur yalnız telefonda (300ms
// gecikme, gri parlama, sayfanın modal arkasında kayması, barın altına
// gizlenen başlık) görünür.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const SRC = resolve(__dirname, '..');
const RAW = readFileSync(resolve(__dirname, 'globals.css'), 'utf8');
// Yorumları karakter-karakter boşalt (ofsetler korunur; depo idiyomu).
const CSS = RAW.replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '));
const read = (rel: string) => readFileSync(resolve(SRC, rel), 'utf8');

interface Rule { selectors: string[]; body: string; at: string[] }

function parse(css: string): Rule[] {
  const out: Rule[] = [];
  const stack: string[] = [];
  let buf = '';
  for (let i = 0; i < css.length; i++) {
    const c = css[i];
    if (c === '{') {
      const prelude = buf.trim();
      buf = '';
      if (prelude.startsWith('@')) { stack.push(prelude); continue; }
      const j = css.indexOf('}', i + 1);
      out.push({
        selectors: splitSelectors(prelude),
        body: css.slice(i + 1, j),
        at: [...stack],
      });
      i = j;
    } else if (c === '}') {
      stack.pop();
      buf = '';
    } else if (c === ';') {
      buf = '';
    } else {
      buf += c;
    }
  }
  return out;
}

// Parantez içindeki virgülü (`:where(a, b)`) bölmeden ayır.
function splitSelectors(prelude: string): string[] {
  const out: string[] = [];
  let depth = 0; let cur = '';
  for (const ch of prelude) {
    if (ch === '(') depth++;
    if (ch === ')') depth--;
    if (ch === ',' && depth === 0) { out.push(cur); cur = ''; continue; }
    cur += ch;
  }
  out.push(cur);
  return out.map(s => s.trim().replace(/\s+/g, ' ')).filter(Boolean);
}

const RULES = parse(CSS);
const BASE = RULES.filter(r => r.at.length === 0);
const decl = (body: string, prop: string) =>
  new RegExp(`(?:^|[;\\s])${prop}\\s*:\\s*([^;]+)`).exec(body)?.[1].trim();

const TOUCH_SEL = ':where(button, a, .btn-icon, [role="button"], summary, label[for])';
// `.pick-list` (Combobox/PickerPopover listesi) kendi kuralında zaten contain
// taşıyordu (v0.10.1089); burada o da çiviye alınıyor.
const OVERLAYS = ['.modal-dialog', '.drawer-panel', '.ui-popover', '.dt-menu-pop', '.cb-list', '.pc-panel', '.pick-list'];
// Çapa hedefleri ve kaynakta bu id'yi/sınıfı taşıyan dosya.
const ANCHORS: Array<[string, string, string]> = [
  ['.sec-head[id]', 'components/ui/SectionHead.tsx', 'className="dtl-sech sec-head" id={id}'],
  ['#dtl-props', 'pages/Service.tsx', 'id="dtl-props"'],
  ['#deploys', 'pages/Service.tsx', 'id="deploys"'],
  ['#db-errors', 'pages/databases/databaseParam.ts', "DATABASE_ERRORS_ANCHOR = 'db-errors'"],
  ['#problem-root-cause', 'features/anomalies/hypothesisView.ts', "ROOT_CAUSE_ANCHOR = 'problem-root-cause'"],
];

describe('v0.10.1339 — platform CSS kazanımları', () => {
  it('1 — dokunma: manipulation + tap-highlight, özgüllük 0 (:where)', () => {
    const r = BASE.find(x => x.selectors.length === 1 && x.selectors[0] === TOUCH_SEL);
    expect(r, `${TOUCH_SEL} taban kuralı kayboldu`).toBeDefined();
    expect(decl(r!.body, 'touch-action')).toBe('manipulation');
    expect(decl(r!.body, '-webkit-tap-highlight-color')).toBe('transparent');
    // Özgüllük taşıyan (`:where` dışı) bir manipulation kuralı tutamakları ezebilir.
    const loud = BASE.filter(x => decl(x.body, 'touch-action') === 'manipulation'
      && x.selectors.some(s => !s.startsWith(':where(')));
    expect(loud.map(x => x.selectors.join(', '))).toEqual([]);
  });

  it('2 — sürükleme tutamakları touch-action: none tutuyor', () => {
    for (const sel of ['thead th .col-resize-handle', '.span-panel-resizer']) {
      const r = BASE.find(x => x.selectors.includes(sel) && decl(x.body, 'touch-action'));
      expect(r, `${sel} touch-action kaybetti`).toBeDefined();
      expect(decl(r!.body, 'touch-action')).toBe('none');
    }
  });

  it('3 — katmanlar overscroll-behavior: contain ve sınıflar canlı', () => {
    const contained = new Set(BASE
      .filter(x => decl(x.body, 'overscroll-behavior') === 'contain')
      .flatMap(x => x.selectors));
    expect(OVERLAYS.filter(s => !contained.has(s)), 'contain kaybolmuş').toEqual([]);
    const owners: Record<string, string> = {
      '.modal-dialog': 'components/ui/Modal.tsx',
      '.drawer-panel': 'components/ui/Drawer.tsx',
      '.ui-popover': 'components/ui/Popover.tsx',
      '.dt-menu-pop': 'components/ui/DataTable/HeadMenu.tsx',
      '.cb-list': 'components/ai/NameCompletionPopup.tsx',
      '.pick-list': 'components/ui/PickerPopover.tsx',
      '.pc-panel': 'components/coremetry/PageControls.tsx',
    };
    for (const sel of OVERLAYS) {
      expect(read(owners[sel]), `${sel} ${owners[sel]} içinde artık yok`).toContain(sel.slice(1));
    }
  });

  it('4 — çapa hedefleri yapışkan barın altına gizlenmiyor', () => {
    for (const [sel, file, needle] of ANCHORS) {
      const r = BASE.find(x => x.selectors.includes(sel) && decl(x.body, 'scroll-margin-top'));
      expect(r, `${sel} scroll-margin-top kaybetti`).toBeDefined();
      expect(decl(r!.body, 'scroll-margin-top')).toMatch(/var\(--controls-h, 0px\)/);
      expect(read(file), `${sel} hedefi ${file} içinde yok`).toContain(needle);
    }
    // --controls-h hâlâ PageControls tarafından yayınlanıyor.
    expect(read('components/coremetry/PageControls.tsx')).toContain("setProperty('--controls-h'");
  });

  it('5 — iOS yakınlaştırma: telefon katmanında form alanları 16px', () => {
    const phone = RULES.filter(x => x.at.some(a => a === '@media (max-width: 640px)'));
    const r = phone.find(x => ['input', 'select', 'textarea'].every(s => x.selectors.includes(s)));
    expect(r, '≤640px input/select/textarea kuralı kayboldu').toBeDefined();
    expect(decl(r!.body, 'font-size')).toBe('16px');
    expect(r!.selectors).toContain('[data-density] .controls input');
  });

  // v0.10.1363 — operatör onayı: kök dikeyde zincirlenmez, yatay serbest.
  it('6 — kök overscroll-behavior-y: none, x ve kısaltma yok', () => {
    const root = RULES.filter(x => x.at.length === 0
      && x.selectors.some(s => s === 'html' || s === ':root' || s === 'body'));
    const y = root.filter(x => decl(x.body, 'overscroll-behavior-y') === 'none');
    expect(y.map(x => x.selectors.join(', '))).toEqual(['html, body']);
    const bad = root.filter(x => decl(x.body, 'overscroll-behavior(?:-x)?'));
    expect(bad.map(x => x.selectors.join(', '))).toEqual([]);
  });
});
