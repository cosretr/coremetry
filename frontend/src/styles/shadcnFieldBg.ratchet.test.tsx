// @vitest-environment jsdom
//
// shadcnFieldBg.ratchet.test.tsx — v0.10.2143 (operator-reported 2026-11-14):
// Problems sayfasındaki "Ekip" açılırı (shadcn NativeSelect) gri sayfada gri
// çiziliyordu. Kök neden: üretilen shadcn alanları `bg-transparent` taşır
// (dark şemada `dark:bg-input/30`); v0.10.1423 "alanlar --field-bg" kararını
// çağıran / bileşik `bg-field dark:bg-field` ile ekler (shadcn-phase1-plan
// §2.2) — 24 kullanım bunu eklememişti.
//
// NE ÇİVİLİYOR:
//   1. components/shadcn dışındaki her <Input> / <Textarea> / <NativeSelect> /
//      <SelectTrigger> kullanımı `bg-field` VE `dark:bg-field` taşır (doğrudan
//      aynı dosyadaki bir sınıf sabitiyle ya da coremetry FIELD_SURFACE ile —
//      Tailwind kaynağı olmayan sayfalar yardımcı sınıf yazamaz). İstisna gerekçeli listede.
//   2. Sınıf GERÇEK DOM'da alanın kendisine iner: select[data-slot=
//      "native-select"] (sarmalayıcıya değil; asChild/data-slot ezilmesi yok).
//   3. Global `select:focus-visible` dış çizgisi shadcn NativeSelect'i
//      dışlar — tek odak göstergesi shadcn halkası.
// jsdom Tailwind CSS'ini yüklemez: hesaplanmış zemin burada ÖLÇÜLEMEZ; kaynak
// + seçici pinidir. Tarayıcı ölçümü sürüm notunda (üç tema, Chromium).
import { describe, it, expect, afterEach } from 'vitest';
import { readLegacyCss } from './legacyCss';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { resolve, join, relative, sep } from 'node:path';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import { Input } from '@/components/shadcn/input';
import { FIELD_SURFACE } from '@/components/coremetry/Field/fieldSurface';

const SRC = resolve(__dirname, '..');
const SKIP = [join('components', 'shadcn') + sep, join('pages', 'design') + sep];
// Gerekçeli istisnalar (dosya → neden).
const EXEMPT: Record<string, string> = {
  'components/coremetry/metrics/MetricsQLEditor.tsx': 'renklendirilmiş katmanın ÜSTÜNDE saydam metin alanı; zemin sarmalayıcıda',
};
const TAGS = /<(NativeSelect|SelectTrigger|Input|Textarea)\b/g;

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (p.endsWith('.tsx') && !/\.test\.tsx$/.test(p)) out.push(p);
  }
  return out;
}
/** JSX açılış etiketinin sonu: `{}` ve tırnak içindeki `>` sayılmaz. */
export function tagEnd(s: string, from: number): number {
  let depth = 0; let quote = '';
  for (let i = from; i < s.length; i++) {
    const c = s[i];
    if (quote) { if (c === quote) quote = ''; continue; }
    if (c === '"' || c === "'" || c === '`') quote = c;
    else if (c === '{') depth++;
    else if (c === '}') depth--;
    else if (c === '>' && depth === 0) return i;
  }
  return s.length;
}
export function missingFieldBg(src: string): string[] {
  const consts = [...src.matchAll(/const (\w+) = ([^;\n]*)/g)]
    .filter(m => /\bbg-field\b/.test(m[2]) && m[2].includes('dark:bg-field')).map(m => m[1]);
  const out: string[] = [];
  for (const m of src.matchAll(TAGS)) {
    const start = m.index! + m[0].length;
    const body = src.slice(start, tagEnd(src, start));
    const direct = /(^|[\s"'`])bg-field\b/.test(body) && body.includes('dark:bg-field');
    const viaConst = [...consts, 'FIELD_SURFACE'].some(c => new RegExp(`\\b${c}\\b`).test(body));
    if (!direct && !viaConst) out.push(`<${m[1]}> satır ${src.slice(0, m.index!).split('\n').length}`);
  }
  return out;
}

describe('shadcn alanları --field-bg zemini alır', () => {
  it('tarayıcı yardımcıları: çok satırlı etiket, sabit, eksik', () => {
    expect(missingFieldBg('<Input className="h-8 bg-field dark:bg-field" onChange={e => f(e)} />')).toEqual([]);
    expect(missingFieldBg("const CTL = 'h-8 bg-field dark:bg-field';\n<Input\n  className={CTL} />")).toEqual([]);
    expect(missingFieldBg('<Input className="h-8" />')).toEqual(['<Input> satır 1']);
    expect(missingFieldBg('<NativeSelect value={a > b ? a : b}>')).toEqual(['<NativeSelect> satır 1']);
    expect(missingFieldBg('<Input className="bg-field" />')).toEqual(['<Input> satır 1']); // dark şema eksik
  });

  it('shadcn dışındaki her alan kullanımı bg-field + dark:bg-field taşır', () => {
    const offenders: string[] = [];
    let scanned = 0;
    for (const p of walk(SRC)) {
      const rel = relative(SRC, p).split(sep).join('/');
      if (SKIP.some(d => relative(SRC, p).startsWith(d)) || rel in EXEMPT) continue;
      const src = readFileSync(p, 'utf8');
      if (!/from '@\/components\/shadcn\/(native-select|select|input|textarea)'/.test(src)) continue;
      scanned++;
      for (const miss of missingFieldBg(src)) offenders.push(`${rel}: ${miss}`);
    }
    expect(scanned).toBeGreaterThan(20);
    expect(offenders, 'alan gri sayfada saydam kalır — className\'e "bg-field dark:bg-field" ekleyin').toEqual([]);
  });

  it('istisna listesi yaşayan dosyalara işaret eder', () => {
    for (const f of Object.keys(EXEMPT)) expect(() => readFileSync(join(SRC, f), 'utf8'), f).not.toThrow();
  });

  it('operatörün bildirdiği yer: Problems "Ekip" açılırı', () => {
    const inbox = readFileSync(join(SRC, 'pages/Inbox.tsx'), 'utf8');
    expect(inbox).toContain("className={cn('ib-team-select', FIELD_SURFACE)}");
    expect(FIELD_SURFACE).toBe('bg-field dark:bg-field');
  });
});

describe('sınıf gerçek DOM\'da alanın kendisinde', () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  let host: HTMLDivElement | null = null;
  let root: Root | null = null;
  afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; });

  it('NativeSelect: select[data-slot="native-select"] bg-field taşır; sarmalayıcı taşımaz', () => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => {
      root!.render(
        <>
          <NativeSelect size="sm" aria-label="Ekip" className={`ib-team-select ${FIELD_SURFACE}`} defaultValue="">
            <NativeSelectOption value="">Ekip: hepsi</NativeSelectOption>
          </NativeSelect>
          <Input aria-label="ara" className="bg-field dark:bg-field" />
        </>,
      );
    });
    const sel = host.querySelector('select')!;
    expect(sel.matches('select[data-slot="native-select"].bg-field.dark\\:bg-field')).toBe(true);
    // bg-field, bg-transparent'tan SONRA: tailwind-merge çakışmayı çözer.
    expect(sel.classList.contains('bg-transparent')).toBe(false);
    expect(host.querySelector('[data-slot="native-select-wrapper"]')!.classList.contains('bg-field')).toBe(false);
    const input = host.querySelector('input')!;
    expect(input.matches('input[data-slot="input"].bg-field')).toBe(true);
    expect(input.classList.contains('bg-transparent')).toBe(false);
  });
});

describe('tek odak göstergesi', () => {
  const css = readLegacyCss().replace(/\/\*[\s\S]*?\*\//g, '');
  const ns = readFileSync(join(SRC, 'components/shadcn/native-select.tsx'), 'utf8');
  it('global select dış çizgisi shadcn NativeSelect\'i dışlar', () => {
    expect(css).toContain('select:where(:not([data-slot="native-select"])):focus-visible,');
    expect(css).not.toMatch(/(^|[\s,])select:focus-visible/);
  });
  it('dışlama seçicisi gerçek öğeyle eşleşir (data-slot ezilmez)', () => {
    const host = document.createElement('div');
    document.body.appendChild(host);
    const root = createRoot(host);
    act(() => { root.render(<NativeSelect aria-label="x" className="bg-field dark:bg-field" />); });
    const sel = host.querySelector('select')!;
    expect(sel.matches('select:where(:not([data-slot="native-select"]))')).toBe(false);
    const legacy = document.createElement('select');
    expect(legacy.matches('select:where(:not([data-slot="native-select"]))')).toBe(true);
    act(() => { root.unmount(); });
    host.remove();
  });
  it('shadcn halkası ve outline-none bileşende durur', () => {
    expect(ns).toContain('outline-none');
    expect(ns).toContain('focus-visible:ring-[3px]');
  });
});
