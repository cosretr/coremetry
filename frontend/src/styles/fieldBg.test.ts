// fieldBg.test.ts — v0.10.1423 (operatör, prod Traces ekranı, redhat tema:
// "Filter service ve filtre kısmı gri değil de beyaza yakın bir renk olsa
// daha belli olur." → "Mockup'tan A seçeneğini yapalım.").
//
// Seçenek A: açık temalarda (redhat — prod varsayılanı — ve light) alanlar
// BEYAZ, kenarlık bugünkü --border, odak halkası aynı. Dark bugünkü değerinde
// (--bg2) kalır. Tek token: --field-bg, tema başına tanımlı; alan dolgusu
// kullanan her kural onu okur (uygulama geneli, yalnız Traces değil).

import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { readdirSync, statSync } from 'node:fs';

const CSS = readFileSync(resolve(__dirname, 'globals.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');

function block(re: RegExp): string {
  const m = re.exec(CSS);
  if (!m) throw new Error(`blok yok: ${re}`);
  return m[1];
}
function decl(body: string, name: string): string | undefined {
  const m = new RegExp(`(?:^|[;{\\s])${name}\\s*:\\s*([^;]+);`).exec(body);
  return m?.[1].trim();
}
const THEMES = {
  dark: block(/:root\s*\{([\s\S]*?)\n\}/),
  light: block(/\[data-theme="light"\]\s*\{([\s\S]*?)\n\}/),
  redhat: block(/\[data-theme="redhat"\]\s*\{([\s\S]*?)\n\}/),
};
const SIDEBAR = block(/\[data-theme="redhat"\] #sidebar\s*\{([\s\S]*?)\n\}/);

// Seçicinin TAM eşleşen kural gövdeleri (virgüllü listeler dahil).
function rulesFor(selector: string): string[] {
  const out: string[] = [];
  for (const m of CSS.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    // v0.10.1611 (shadcn Faz 1 R4) — öğe kuralları `:where(:not([data-slot]))`
    // korumasını taşır (özgüllük 0); taban seçici aynı kuraldır.
    const sels = m[1].split(',').map(s => s.trim().split(':where(:not([data-slot]))').join(''));
    if (sels.includes(selector)) out.push(m[2]);
  }
  return out;
}
function backgroundOf(selector: string): string | undefined {
  for (const body of rulesFor(selector)) {
    const v = decl(body, 'background(?:-color)?');
    if (v) return v;
  }
  return undefined;
}

describe('--field-bg her temada tanımlı', () => {
  it.each(Object.keys(THEMES) as (keyof typeof THEMES)[])('%s bloğu --field-bg tanımlıyor', t => {
    expect(decl(THEMES[t], '--field-bg'), `${t}: --field-bg yok`).toBeDefined();
  });

  it('redhat ve light: alan BEYAZ (--bg1 = #ffffff)', () => {
    for (const t of ['redhat', 'light'] as const) {
      expect(decl(THEMES[t], '--field-bg')).toBe('var(--bg1)');
      expect(decl(THEMES[t], '--bg1')?.toLowerCase()).toBe('#ffffff');
    }
  });

  it('dark: bugünkü değer aynen (--bg2 = #2c3035)', () => {
    expect(decl(THEMES.dark, '--field-bg')).toBe('var(--bg2)');
    expect(decl(THEMES.dark, '--bg2')?.toLowerCase()).toBe('#2c3035');
  });

  it('redhat koyu sidebar: alanlar rayın kendi --bg2si (html’deki beyaz miras kalmaz)', () => {
    expect(decl(SIDEBAR, '--field-bg')).toBe('var(--bg2)');
  });

  it('--field-bg yalnız tema bloklarında tanımlanır (4 tanım, renk literali yok)', () => {
    const defs = [...CSS.matchAll(/--field-bg\s*:\s*([^;]+);/g)].map(m => m[1].trim());
    expect(defs).toHaveLength(4);
    for (const v of defs) expect(v).toMatch(/^var\(--bg[12]\)$/);
  });
});

describe('alan kontrolleri --field-bg okuyor', () => {
  it.each([
    'input',          // taban `input, select` — Combobox / ServicePicker / OperationPicker girdisi buradan
    'select',
    'select option',
    'textarea',
    '.fq-bar',        // FilterQueryBox barı (Traces "Filtre")
    '.tb-search',     // topbar genel arama kutusu
    '.fsel-btn',      // FacetMultiSelect tetikleyicisi
    '.adv-query textarea', // Explore → Advanced DSL editörü
    '.mqe-code textarea',  // metrik sorgu kod editörü
  ])('%s zemini var(--field-bg)', sel => {
    expect(backgroundOf(sel), `${sel}`).toBe('var(--field-bg)');
  });

  it('seçici girdileri kendi zeminini YAZMAZ — tabandan miras alır', () => {
    for (const body of rulesFor('.cb-wrap input')) {
      expect(decl(body, 'background(?:-color)?')).toBeUndefined();
    }
  });

  it('kenarlık ve odak değişmedi: --border, odakta --focus', () => {
    const base = rulesFor('input').find(b => /background/.test(b))!;
    expect(decl(base, 'border')).toBe('1px solid var(--border)');
    expect(rulesFor('input:focus').some(b => decl(b, 'border-color') === 'var(--focus)')).toBe(true);
    expect(decl(rulesFor('.fq-bar').find(b => /background/.test(b))!, 'border')).toBe('1px solid var(--border)');
  });
});

describe('Trace ID kutusunun CSS ailesi silindi', () => {
  it('.trace-lookup / .tl-icon kuralı kalmadı', () => {
    expect(CSS).not.toMatch(/\.trace-lookup\b/);
    expect(CSS).not.toMatch(/\.tl-icon\b/);
  });
});

// v0.10.1423 (operatör onayı: "Uygula, search'e de uygula") — HER arama
// kutusu aynı tokeni okur: topbar genel arama (.tb-search, yukarıda), topbar
// env/cluster seçicileri, Trace detayının "Filter spans…" kutusu, sayfa
// aramaları (Services/Endpoints SearchField + ServicePicker, Logs sorgu barı,
// Metrics ad seçicisi, Ayarlar aramaları) ve komut paleti girdisi.
const SRC = resolve(__dirname, '..');
const readSrc = (rel: string) => readFileSync(join(SRC, rel), 'utf8');

describe('arama kutuları --field-bg okuyor', () => {
  it('tabandan miras alanların sınıfları kendi zeminini yazmaz', () => {
    for (const sel of ['.sf-input', '.sf-wrap .sf-input', '.cb-wrap input', '.env-pick-wrap', '.env-pick-wrap input',
      '.ctx-field select', '.ctx-field input', '.ctx-disabled']) {
      for (const body of rulesFor(sel)) {
        expect(decl(body, 'background(?:-color)?'), sel).toBeUndefined();
      }
    }
  });

  it('atom / seçici girdileri satır içi zemin yazmaz (taban `input` kuralı)', () => {
    for (const rel of ['components/ui/SearchField.tsx', 'components/Combobox.tsx', 'components/KqlSearchInput.tsx',
      'components/ServicePicker.tsx', 'components/OperationPicker.tsx', 'components/MetricNamePicker.tsx',
      'components/EnvPicker.tsx', 'components/ContextBar/ContextBar.tsx']) {
      const src = readSrc(rel);
      const inputs = [...src.matchAll(/<(input|select|textarea)\b[\s\S]*?\/?>/g)].map(m => m[0]);
      for (const tag of inputs) expect(tag, rel).not.toMatch(/background:/);
    }
  });

  it('Trace detayı "Filter spans…" ve komut paleti girdileri --field-bg', () => {
    const trace = readSrc('pages/Trace.tsx');
    const i = trace.indexOf('placeholder="Filter spans (name, service, attr value)…"');
    expect(i).toBeGreaterThan(0);
    expect(trace.slice(i, trace.indexOf('/>', i))).toContain("background: 'var(--field-bg)'");
    const cp = readSrc('components/CommandPalette.tsx');
    expect((cp.match(/background: 'var\(--field-bg\)'/g) ?? []).length).toBe(3);
    expect(cp).not.toMatch(/outline: 'none',\s*background: 'transparent'/);
    expect(readSrc('components/TopbarSearch.tsx')).toMatch(/className="tb-search"/);
  });

  // Mandal: satır içi zemin yazan HER input/select/textarea --field-bg yazar.
  // Gri `--bg2` / sayfa `--bg0` / dark'ta kart rengi `--bg` alanı zemine
  // karıştırıyordu (Logs "Filter the N loaded rows" kutusu --bg0'dı).
  it('TSX: satır içi zemin yazan alan elemanı yalnız var(--field-bg) yazar', () => {
    const offenders: string[] = [];
    const walk = (dir: string) => {
      for (const f of readdirSync(dir)) {
        const p = join(dir, f);
        if (statSync(p).isDirectory()) { walk(p); continue; }
        if (!f.endsWith('.tsx') || f.includes('.test.')) continue;
        const s = readFileSync(p, 'utf8');
        for (const m of s.matchAll(/<(input|select|textarea)\b/g)) {
          // etiket sonu: süslü parantez derinliği 0'daki ilk '>'
          let depth = 0, end = m.index! + m[0].length;
          for (; end < s.length; end++) {
            const ch = s[end];
            if (ch === '{') depth++;
            else if (ch === '}') depth--;
            else if (ch === '>' && depth === 0) break;
          }
          const tag = s.slice(m.index!, end);
          if (/type="(checkbox|radio|color|range)"/.test(tag)) continue;
          const bg = /background(?:Color)?:\s*([^,}\n]+)/.exec(tag);
          if (bg && bg[1].trim() !== "'var(--field-bg)'") {
            offenders.push(`${p.slice(SRC.length + 1)}:${s.slice(0, m.index).split('\n').length} ${bg[1].trim()}`);
          }
        }
      }
    };
    walk(SRC);
    expect(offenders).toEqual([]);
  });
});
