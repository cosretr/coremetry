// inboxWhenFont.test.ts — v0.10.736 (operatör, prod ekran görüntüsü: "tarih
// fontu biraz daha büyük olabilir"). Inbox First seen / Last seen hücreleri
// 11 px satır-içi stilden 13 px sınıfa; yaş alt satırı 11 px soluk; kolon
// genişlikleri damgayı sığdırır.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const page = readFileSync(resolve(__dirname, 'Inbox.tsx'), 'utf8');
const css = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8');

describe('Inbox tarih hücreleri (v0.10.736)', () => {
  it('iki damga hücresi sınıfta (satır-içi 11 px yok); yaş alt satırı sınıfta', () => {
    expect((page.match(/<td className="mono ib-when">/g) ?? []).length).toBe(2);
    expect(page).not.toContain('<td className="mono" style={{ fontSize: 11 }}>');
    expect(page).toContain('<div className="ib-when__ago">');
  });
  it('CSS: damga --fs-md (13 px), yaş --fs-xs soluk', () => {
    expect(css).toContain('.ib-when { font-size: var(--fs-md); }');
    expect(css).toMatch(/\.ib-when__ago \{ color: var\(--text3\);[^}]*font-size: var\(--fs-xs\)/);
  });
  it('kolon genişlikleri 13 px damgayı sığdırır', () => {
    expect(page).toContain("{ id: 'firstSeen', label: 'First seen', sortValue: it => it.startedAt, naturalDir: 'desc', width: 168 }");
    expect(page).toContain("{ id: 'lastSeen', label: 'Last seen', sortValue: it => it.lastSeen,        naturalDir: 'desc', width: 180 }");
  });
});

// v0.10.739 — aynı sınıf operatör-yüzeyli listelere yayıldı; satır-içi 11 px
// tarih hücresi bu dosyalarda kalmadı.
describe('tarih damgası sınıfı diğer listelerde (v0.10.739)', () => {
  const files: [string, number][] = [
    ['../features/anomalies/AnomaliesPage.tsx', 2],
    ['../features/anomalies/streams.tsx', 2],
    ['Incidents.tsx', 1],
    ['explore/TracesResult.tsx', 1],
  ];
  for (const [f, n] of files) {
    it(`${f}: ${n} hücre .ib-when, satır-içi 11 px tsLong hücresi yok`, () => {
      const src = readFileSync(resolve(__dirname, f), 'utf8');
      // v0.10.945 (tablo standardı S3) — soluk damga rengi satır içi değil sınıfta (cell-faint).
      expect((src.match(/className="mono(?: row-cell)? ib-when(?: cell-(?:faint|muted))?"/g) ?? []).length).toBe(n);
      expect(src).not.toMatch(/<td className="mono(?: row-cell)?" style=\{\{ fontSize: 11[^}]*\}\}>(?:<Link[^>]*>)?\{tsLong\(/);
    });
  }
});

// ── v0.10.933 (tablo standardı T5) — KAZANAN kural 13 px ──────────────────
// Dizginin CSS'te bulunması yetmez: `table .mono { font-size: inherit }`
// (0,1,1) bir sürüm boyunca `.ib-when`'i (0,1,0) sessizce ezdi — damga
// yine 12 px oldu, yukarıdaki toContain yeşil kaldı. Burada küçük bir
// kaskad: sayfadaki tablonun `td.mono.ib-when` hücresine (varsayılan
// yoğunluk, @media dışı) boyut yazan her kural bulunur; özgüllük + dosya
// sırası kazananı seçer. Kapsam bilerek dar: ata zinciri html → body →
// table → tbody → tr → td; durum/yapı sözde sınıfları (:hover, :first-child…)
// eşleşmez sayılır.
interface El { tag: string; classes: string[]; attrs: Record<string, string> }
const CHAIN: El[] = [
  { tag: 'html', classes: [], attrs: { 'data-density': 'comfortable' } },
  { tag: 'body', classes: [], attrs: {} },
  { tag: 'table', classes: [], attrs: {} },
  { tag: 'tbody', classes: [], attrs: {} },
  { tag: 'tr', classes: [], attrs: {} },
  { tag: 'td', classes: ['mono', 'ib-when'], attrs: {} },
];
type Spec = [number, number, number];
const add = (a: Spec, b: Spec): Spec => [a[0] + b[0], a[1] + b[1], a[2] + b[2]];
const cmp = (a: Spec, b: Spec) => a[0] - b[0] || a[1] - b[1] || a[2] - b[2];

// Parantez/köşeli parantez dışındaki ayraçlarda böl (`:where(a, b)` tek parça).
function splitTop(s: string, seps: string): { sep: string; part: string }[] {
  const out: { sep: string; part: string }[] = [];
  let depth = 0; let cur = ''; let sep = '';
  for (const c of s) {
    if (c === '(' || c === '[') depth++;
    else if (c === ')' || c === ']') depth--;
    if (depth === 0 && seps.includes(c)) {
      if (cur.trim()) { out.push({ sep, part: cur.trim() }); cur = ''; sep = ''; }
      if (c.trim()) sep = c;
      continue;
    }
    cur += c;
  }
  if (cur.trim()) out.push({ sep, part: cur.trim() });
  return out;
}

// Tek bileşik seçici (`td.mono:where(table)`) → eşleşme + özgüllük.
function compound(comp: string, el: El): { ok: boolean; spec: Spec } {
  let ok = true; let spec: Spec = [0, 0, 0]; let rest = comp;
  while (rest) {
    let m: RegExpExecArray | null;
    if ((m = /^\*/.exec(rest))) { /* evrensel */ }
    else if ((m = /^[a-z][\w-]*/i.exec(rest))) { ok &&= m[0].toLowerCase() === el.tag; spec = add(spec, [0, 0, 1]); }
    else if ((m = /^\.([\w-]+)/.exec(rest))) { ok &&= el.classes.includes(m[1]); spec = add(spec, [0, 1, 0]); }
    else if ((m = /^#[\w-]+/.exec(rest))) { ok = false; spec = add(spec, [1, 0, 0]); }
    else if ((m = /^\[([\w-]+)(?:=["']?([^"'\]]*)["']?)?\]/.exec(rest))) {
      ok &&= m[1] in el.attrs && (m[2] === undefined || el.attrs[m[1]] === m[2]);
      spec = add(spec, [0, 1, 0]);
    } else if ((m = /^::?([\w-]+)(?:\(((?:[^()]|\([^()]*\))*)\))?/.exec(rest))) {
      const [, name, arg] = m;
      if (m[0].startsWith('::')) { ok = false; spec = add(spec, [0, 0, 1]); }
      else if ((name === 'where' || name === 'is' || name === 'not') && arg !== undefined) {
        const inner = splitTop(arg, ',').map(({ part }) => compound(part, el));
        const any = inner.some(r => r.ok);
        ok &&= name === 'not' ? !any : any;
        if (name !== 'where') spec = add(spec, inner.map(r => r.spec).sort(cmp).at(-1) ?? [0, 0, 0]);
      } else { ok = false; spec = add(spec, [0, 1, 0]); }
    } else throw new Error(`çözülemeyen seçici parçası: ${rest}`);
    rest = rest.slice(m[0].length);
  }
  return { ok, spec };
}

// Tam seçici CHAIN'in son öğesine (td) eşleşiyor mu? Sağdan sola.
function matches(sel: string): { ok: boolean; spec: Spec } {
  const parts = splitTop(sel, ' >+~');
  const spec = parts.reduce<Spec>((a, p) => add(a, compound(p.part, CHAIN[0]).spec), [0, 0, 0]);
  const walk = (pi: number, ei: number): boolean => {
    if (!compound(parts[pi].part, CHAIN[ei]).ok) return false;
    if (pi === 0) return true;
    const comb = parts[pi].sep;
    if (comb === '>') return ei > 0 && walk(pi - 1, ei - 1);
    if (comb === '+' || comb === '~') return false;
    for (let j = ei - 1; j >= 0; j--) if (walk(pi - 1, j)) return true;
    return false;
  };
  return { ok: walk(parts.length - 1, CHAIN.length - 1), spec };
}

describe('tablo içindeki .ib-when boyutu KAZANIR (v0.10.933, T5)', () => {
  const CLEAN = css.replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '));
  // Kurallar dosya sırasıyla; @media / @supports içindekiler koşullu → dışarıda.
  const decls: { selector: string; value: string; spec: Spec; order: number; important: boolean }[] = [];
  let order = 0; let atDepth = 0; let start = 0;
  for (let i = 0; i < CLEAN.length; i++) {
    const c = CLEAN[i];
    if (c === ';') { start = i + 1; continue; }
    if (c === '}') { atDepth--; start = i + 1; continue; }
    if (c !== '{') continue;
    const head = CLEAN.slice(start, i).trim();
    if (head.startsWith('@')) { atDepth++; start = i + 1; continue; }
    const end = CLEAN.indexOf('}', i);
    const body = CLEAN.slice(i + 1, end);
    if (atDepth === 0) {
      for (const { part: selector } of splitTop(head.replace(/\s+/g, ' '), ',')) {
        const r = matches(selector);
        if (!r.ok) continue;
        for (const d of body.matchAll(/(?:^|;)\s*font-size\s*:\s*([^;]+)/g)) {
          const important = /!important/.test(d[1]);
          decls.push({ selector, value: d[1].replace(/!important/, '').trim(), spec: r.spec, order: order++, important });
        }
        if (/(?:^|;)\s*font\s*:/.test(body)) decls.push({ selector, value: 'font-shorthand', spec: r.spec, order: order++, important: false });
      }
    }
    i = end; start = end + 1;
  }

  it('eşleyici BAYAT değil: taban .mono, :where(table) .mono ve .ib-when adayda', () => {
    const sels = decls.map(d => d.selector);
    expect(sels).toContain('.mono');
    expect(sels).toContain(':where(table) .mono');
    expect(sels).toContain('.ib-when');
    expect(decls.find(d => d.selector === ':where(table) .mono')!.spec).toEqual([0, 1, 0]);
  });

  it('kazanan `.ib-when` → var(--fs-md) = 13 px', () => {
    const winner = [...decls].sort((a, b) =>
      Number(a.important) - Number(b.important) || cmp(a.spec, b.spec) || a.order - b.order).at(-1)!;
    expect(winner.selector, `kazanan: ${winner.selector} { font-size: ${winner.value} }`).toBe('.ib-when');
    expect(winner.value).toBe('var(--fs-md)');
    expect(css).toMatch(/--fs-md:\s*13px;/);
  });
});
