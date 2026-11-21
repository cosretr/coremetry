// effectFetch ratchet — bileşen içinde `useEffect` + `api.*` ile veri çekme
// yalnız AZALIR. Yeni veri yüzeyi `lib/queries/*` hook'u (React Query,
// AbortSignal, anahtar her girdiyi taşır) kullanır; elle effect-fetch
// yarış/iptal/çift-istek hatalarının sınıfıdır (StrictMode çift effect,
// unmount sonrası setState, eski cevabın yenisini ezmesi).
//
// Sayılan: components/ + pages/ altında (test hariç) en az bir `useEffect(`
// gövdesinde `api.` çağrısı geçen DOSYA sayısı. Sayı düştüyse TAVANI da
// düşür (bayat tavan = yeni ihlale yer açar).
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { resolve, join } from 'node:path';

const SRC = resolve(__dirname, '../..');

// 2026-11-21: ölçülen 56 → ilk dilimde (SpanDetail, LogContextModal, Logs
// yan panelleri/rail; Trace paylaşım popover'ı) 53. pages/Trace.tsx listede
// kalıyor: ana trace yüklemesi (api.trace) panel değil, sonraki dilim.
const CEILING = 52;

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name)) out.push(p);
  }
  return out;
}

/** Her `useEffect(` çağrısının parantez içi gövdesini döndürür. */
function effectBodies(src: string): string[] {
  const out: string[] = [];
  const re = /\buseEffect\s*\(/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(src))) {
    let depth = 1;
    let i = m.index + m[0].length;
    const start = i;
    for (; i < src.length && depth > 0; i++) {
      const c = src[i];
      if (c === '(') depth++;
      else if (c === ')') depth--;
    }
    out.push(src.slice(start, i - 1));
  }
  return out;
}

// Yorumlar atılır: açıklama cümlesindeki "api.logs(" ihlal sayılmasın
// (cancellation.test.ts stripComments dersi).
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
}

function fetchesInEffect(raw: string): boolean {
  const src = stripComments(raw);
  return effectBodies(src).some(b => /(^|[^\w.])api\.\w+\s*\(/.test(b));
}

function offenders(): string[] {
  const files = [...walk(join(SRC, 'components')), ...walk(join(SRC, 'pages'))];
  return files
    .filter(f => fetchesInEffect(readFileSync(f, 'utf8')))
    .map(f => f.slice(SRC.length + 1).split('\\').join('/'))
    .sort();
}

describe('effectFetch ratchet — useEffect içinde api.* yalnız azalır', () => {
  it('tarayıcı gerçekten yakalıyor (kendi kendini doğrulama)', () => {
    expect(fetchesInEffect('useEffect(() => { api.getSpan(id).then(setS); }, [id]);')).toBe(true);
    expect(fetchesInEffect('useEffect(() => { if (x) { void api.logs({ q: f(a) }); } }, [x]);')).toBe(true);
    expect(fetchesInEffect('const q = useSpan(id); useEffect(() => { setOpen(false); }, [id]);')).toBe(false);
    expect(fetchesInEffect('useEffect(() => { myapi.get(); x.api.get(); }, []);')).toBe(false);
    expect(fetchesInEffect('api.getSpan(id); useMemo(() => 1, []);')).toBe(false);
  });

  it(`sayı tavanı aşmıyor (tavan ${CEILING})`, () => {
    const list = offenders();
    expect(list.length, `useEffect içinde api.* — lib/queries hook'u kullan:\n${list.join('\n')}`)
      .toBeLessThanOrEqual(CEILING);
    expect(list.length, `sayı ${list.length}'e düştü — CEILING'i de düşür`).toBe(CEILING);
  });
});
